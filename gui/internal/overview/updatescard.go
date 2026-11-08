package overview

import (
	"cmp"
	"strings"
	"time"

	"github.com/Enzord758/archmaid/gui/internal/plural"
)

// The Overview's Updates card: what updates-cache.json says, and the
// "Check for updates" button's state while it waits for a new result. Plain
// Go: the window owns the clock and the widgets. Nothing here runs as root
// or reaches pacman; the check itself is driver.RefreshUpdates, the timer's
// own unit.

// CheckTimeout is how long the card waits for the cache to change after the
// check was started (the script has its own 120 s yay timeout).
const CheckTimeout = 2 * time.Minute

// reasonMax cuts a failure reason to a cell's line.
const reasonMax = 80

type cardState int

const (
	cardIdle cardState = iota
	cardChecking
	cardFailed
)

// UpdatesCard is the check button's bookkeeping. The zero value is idle.
type UpdatesCard struct {
	// Timeout is how long to wait for a result; zero is CheckTimeout (tests
	// shorten it).
	Timeout time.Duration
	state   cardState
	since   time.Time
	checks  time.Time // the cache's Checked at Begin
	mod     time.Time // its modification time at Begin
	reason  string
}

// Checking: a check was started and no new result has come yet.
func (c *UpdatesCard) Checking() bool { return c.state == cardChecking }

// Begin starts waiting: u and mod are the cache as it is now (the
// baseline a change is measured from).
func (c *UpdatesCard) Begin(u Updates, mod, now time.Time) {
	*c = UpdatesCard{Timeout: c.Timeout, state: cardChecking, since: now, checks: u.Checked, mod: mod}
}

// Clear drops a failure (the card shows the cache again).
func (c *UpdatesCard) Clear() {
	if c.state == cardFailed {
		c.state = cardIdle
	}
}

// Fail ends the wait with a reason (the check could not be started).
func (c *UpdatesCard) Fail(err error) {
	c.state = cardFailed
	c.reason = "Could not start the check: " + cut(firstLine(err.Error()))
}

// Poll looks at the cache while checking. done: the wait is over, because
// the file changed (idle: View shows the new result) or CheckTimeout passed
// (failed). It does nothing when not checking.
func (c *UpdatesCard) Poll(u Updates, mod, now time.Time) (done bool) {
	if c.state != cardChecking {
		return false
	}
	// A result counts when the file changed and was written after the click:
	// a run that was already going (the timer's, right after the GUI started)
	// finishes with a count from before it, which is not what was asked for
	// (#266: "Up to date" with updates waiting).
	if u.Known && (!mod.Equal(c.mod) || !u.Checked.Equal(c.checks)) && !mod.Before(c.since) {
		c.state = cardIdle
		return true
	}
	limit := cmp.Or(c.Timeout, CheckTimeout)
	if now.Sub(c.since) >= limit {
		c.state = cardFailed
		c.reason = "No new result after " + waited(limit)
		return true
	}
	return false
}

// CardView is what the card shows.
type CardView struct {
	// Title is the headline, Detail the line under it ("" for none).
	Title, Detail string
	// Tag is the outcome tag's text ("" hides it), Tone its class suffix:
	// ok, warn, danger or neutral.
	Tag, Tone string
	Spinner   bool
	// Waiting: updates are waiting in a current cache, so Run topgrade is
	// offered.
	Waiting bool
}

// View is the card for the cache u at now.
func (c *UpdatesCard) View(u Updates, now time.Time) CardView {
	base := cacheView(u, now)
	switch c.state {
	case cardChecking:
		return CardView{Title: "Checking…", Detail: base.Detail, Spinner: true}
	case cardFailed:
		return CardView{Title: "Check failed", Detail: c.reason, Tag: "CHECK FAILED", Tone: "danger"}
	}
	return base
}

func cacheView(u Updates, now time.Time) CardView {
	if !u.Known {
		return CardView{Title: "Not checked yet", Tag: "NOT CHECKED", Tone: "neutral"}
	}
	if !u.Fresh(now) {
		age := now.Sub(u.Checked)
		t := "Unknown (check is " + plural.N(int(age.Hours()/24), "day", "days") + " old)"
		switch {
		case age < 0:
			t = "Unknown (check is dated in the future)"
		case age < 48*time.Hour:
			t = "Unknown (check is " + plural.N(int(age.Hours()), "hour", "hours") + " old)"
		}
		return CardView{Title: t, Tag: "UNKNOWN", Tone: "neutral"}
	}
	var parts []string
	if u.AURHeld > 0 {
		parts = append(parts, plural.N(u.AURHeld, "held", "held"))
	}
	parts = append(parts, "checked "+Ago(u.Checked, now))
	v := CardView{Detail: strings.Join(parts, " · ")}
	if u.Count == 0 {
		v.Title, v.Tag, v.Tone = "Up to date", "UP TO DATE", "ok"
		return v
	}
	v.Title, v.Tag, v.Tone, v.Waiting = plural.N(u.Count, "update", "updates")+" waiting", "UPDATES", "warn", true
	return v
}

// waited is "2 minutes" or "30 seconds".
func waited(d time.Duration) string {
	if d >= time.Minute {
		return plural.N(int(d/time.Minute), "minute", "minutes")
	}
	return plural.N(int(d/time.Second), "second", "seconds")
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	return s
}

func cut(s string) string {
	if r := []rune(s); len(r) > reasonMax {
		return string(r[:reasonMax-1]) + "…"
	}
	return s
}
