package overview

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var cardNow = time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)

func upd(count, held int, age time.Duration) Updates {
	return Updates{Known: true, Count: count, Repo: count, AURHeld: held, Checked: cardNow.Add(-age)}
}

func TestUpdatesCardTexts(t *testing.T) {
	var c UpdatesCard
	for _, tc := range []struct {
		name               string
		u                  Updates
		title, detail, tag string
		tone               string
		waiting            bool
	}{
		{"waiting and held", upd(5, 2, 17*time.Minute), "5 updates waiting", "checked 17 min ago", "UPDATES", "warn", true},
		{"one", upd(1, 0, 3*time.Minute), "1 update waiting", "checked 3 min ago", "UPDATES", "warn", true},
		{"up to date", upd(0, 0, 5*time.Hour), "Up to date", "checked 5 h ago", "UP TO DATE", "ok", false},
		{"not checked", Updates{}, "Not checked yet", "", "NOT CHECKED", "neutral", false},
		{"stale days", upd(4, 0, 3*24*time.Hour), "Unknown (check is 3 days old)", "", "UNKNOWN", "neutral", false},
		{"stale hours", upd(4, 0, 30*time.Hour), "Unknown (check is 30 hours old)", "", "UNKNOWN", "neutral", false},
		{"future", upd(4, 0, -time.Hour), "Unknown (check is dated in the future)", "", "UNKNOWN", "neutral", false},
	} {
		v := c.View(tc.u, cardNow)
		if v.Title != tc.title || v.Detail != tc.detail || v.Tag != tc.tag || v.Tone != tc.tone || v.Waiting != tc.waiting || v.Spinner {
			t.Errorf("%s: %+v", tc.name, v)
		}
	}
}

func TestUpdatesCardStaleNeverOffersTopgrade(t *testing.T) {
	// 4 updates in a cache a day and a half old are not "waiting": the
	// count may be long gone.
	if v := (&UpdatesCard{}).View(upd(4, 0, 36*time.Hour), cardNow); v.Waiting {
		t.Fatalf("%+v", v)
	}
}

func TestUpdatesCardChecking(t *testing.T) {
	before := upd(5, 0, 17*time.Minute)
	mod := cardNow.Add(-17 * time.Minute)
	var c UpdatesCard
	c.Begin(before, mod, cardNow)
	v := c.View(before, cardNow)
	if v.Title != "Checking…" || !v.Spinner || v.Waiting || v.Tag != "" || v.Detail != "checked 17 min ago" {
		t.Fatalf("%+v", v)
	}
	// Nothing changed yet.
	if c.Poll(before, mod, cardNow.Add(30*time.Second)) || !c.Checking() {
		t.Fatal("polled done with no change")
	}
	// A file that went missing or unparsable is not a result.
	if c.Poll(Updates{}, time.Time{}, cardNow.Add(31*time.Second)) || !c.Checking() {
		t.Fatal("an unreadable cache counted as a result")
	}
	// The new result.
	after := upd(2, 0, 0)
	if !c.Poll(after, cardNow.Add(40*time.Second), cardNow.Add(41*time.Second)) || c.Checking() {
		t.Fatal("a changed cache did not end the check")
	}
	if v := c.View(after, cardNow.Add(41*time.Second)); v.Title != "2 updates waiting" || v.Spinner {
		t.Fatalf("%+v", v)
	}
}

func TestUpdatesCardSameMinuteRewrite(t *testing.T) {
	// A result with the same counts and the same checked second but a
	// newer file is still a result (mtime).
	u := upd(0, 0, time.Minute)
	var c UpdatesCard
	c.Begin(u, cardNow.Add(-time.Minute), cardNow)
	if !c.Poll(u, cardNow.Add(time.Second), cardNow.Add(2*time.Second)) {
		t.Fatal("new mtime not seen")
	}
}

func TestUpdatesCardTimeout(t *testing.T) {
	u := upd(5, 0, time.Hour)
	mod := cardNow.Add(-time.Hour)
	var c UpdatesCard
	c.Begin(u, mod, cardNow)
	if c.Poll(u, mod, cardNow.Add(CheckTimeout-time.Second)) {
		t.Fatal("gave up early")
	}
	if !c.Poll(u, mod, cardNow.Add(CheckTimeout)) || c.Checking() {
		t.Fatal("did not give up at the timeout")
	}
	v := c.View(u, cardNow.Add(CheckTimeout))
	if v.Title != "Check failed" || v.Tag != "CHECK FAILED" || v.Tone != "danger" || v.Detail != "No new result after 2 minutes" || v.Spinner || v.Waiting {
		t.Fatalf("%+v", v)
	}
	// Failed stays failed, and polls no more; a new Begin starts over.
	if c.Poll(u, mod, cardNow.Add(time.Hour)) {
		t.Fatal("polled after failing")
	}
	c.Begin(u, mod, cardNow.Add(time.Hour))
	if !c.Checking() {
		t.Fatal("no new check after a failure")
	}
}

func TestUpdatesCardStartFailure(t *testing.T) {
	var c UpdatesCard
	c.Fail(errors.New("driver: updates check not started: systemctl: exit status 1 (no user manager); no /usr/lib/archmaid/archmaid-updates-check\nmore"))
	v := c.View(Updates{}, cardNow)
	if v.Title != "Check failed" || !strings.HasPrefix(v.Detail, "Could not start the check: driver: updates") || strings.Contains(v.Detail, "\n") ||
		len([]rune(v.Detail)) > len("Could not start the check: ")+reasonMax {
		t.Fatalf("%+v", v)
	}
}

func TestReadUpdatesStamp(t *testing.T) {
	dir := t.TempDir()
	if u, mod := ReadUpdatesStamp(dir); u.Known || !mod.IsZero() {
		t.Fatalf("%+v %v", u, mod)
	}
	p := filepath.Join(dir, "updates-cache.json")
	if err := os.WriteFile(p, []byte(cacheV3), 0o600); err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 11, 1, 1, 2, 3, 0, time.UTC)
	if err := os.Chtimes(p, want, want); err != nil {
		t.Fatal(err)
	}
	u, mod := ReadUpdatesStamp(dir)
	if !u.Known || u.Count != 10 || !mod.Equal(want) {
		t.Fatalf("%+v %v", u, mod)
	}
}
