package tui

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Enzord758/archmaid/internal/engine"
)

// The model tests answer a prompt the moment it shows, so the typed-ahead
// guard is off for the package; the tests below switch it on with a clock
// of their own.
func init() { confirmArm = 0 }

// armedClock turns the guard on with a clock the test moves; the returned
// func advances it.
func armedClock(t *testing.T) func(time.Duration) {
	t.Helper()
	oldArm, oldNow := confirmArm, armNow
	now := time.Unix(1_000_000, 0)
	confirmArm, armNow = 400*time.Millisecond, func() time.Time { return now }
	t.Cleanup(func() { confirmArm, armNow = oldArm, oldNow })
	return func(d time.Duration) { now = now.Add(d) }
}

// typeAhead sends "hunter2" and Enter as the keys of a password typed before
// its prompt: the 'n' is a No, the Enter the default's answer.
func typeAhead(m model) model {
	for _, r := range "hunter2" {
		m = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return update(m, key("enter"))
}

func topgradeConfirm(m model) model {
	return feed(m,
		engine.QueueStarted{Action: "q", Steps: []engine.PlanStep{{ID: "a", Title: "a…", Confirm: true}}},
		engine.NeedsConfirm{ID: 3, Kind: engine.ConfirmStep, Step: "a", Msg: "Run a system update with topgrade?"})
}

// Keys that arrive as the confirm is drawn (a password typed ahead, with its
// Enter) answer nothing; a y or n after a pause does.
func TestTypedAheadDoesNotAnswerConfirm(t *testing.T) {
	advance := armedClock(t)
	m, f := engineModel()
	m = typeAhead(topgradeConfirm(m))
	if len(f.sent) != 0 || m.screen != screenConfirm || m.confirmYes {
		t.Fatalf("typed-ahead input answered the confirm: sent %+v, screen %d, yes %v", f.sent, m.screen, m.confirmYes)
	}
	// Still typing 300 ms later: still not an answer, the window renewed.
	advance(300 * time.Millisecond)
	m = update(m, key("enter"))
	if len(f.sent) != 0 || m.screen != screenConfirm {
		t.Fatalf("a trailing Enter answered the confirm: sent %+v", f.sent)
	}
	// A deliberate answer after a pause works, both ways.
	advance(500 * time.Millisecond)
	update(m, key("y"))
	if got := lastSent(t, f); got != (engine.Confirm{Prompt: 3, Yes: true}) {
		t.Errorf("deliberate y sent %#v", got)
	}
	m2, f2 := engineModel()
	m2 = topgradeConfirm(m2)
	advance(500 * time.Millisecond)
	update(m2, key("n"))
	if got := lastSent(t, f2); got != (engine.Confirm{Prompt: 3, Yes: false}) {
		t.Errorf("deliberate n sent %#v", got)
	}
}

// The same holds for a confirm that follows the password prompt, and for the audit report (its a accepts).
func TestTypedAheadAfterPasswordAndAudit(t *testing.T) {
	advance := armedClock(t)
	m, f := engineModel()
	m = feed(m, engine.NeedsPassword{ID: 7, Kind: engine.PasswordUpFront})
	if m.screen != screenPassword {
		t.Fatalf("screen %d, want the password prompt", m.screen)
	}
	m = feed(m, engine.AuthResolved{Granted: true})
	m = topgradeConfirm(m) // the keys are still coming in
	m = typeAhead(m)
	if m.screen != screenConfirm || len(f.sent) != 0 {
		t.Fatalf("keys after the password answered the confirm: screen %d, sent %+v", m.screen, f.sent)
	}

	m, f = engineModel()
	m = feed(m, engine.OutputReport{ID: 5, Text: "findings", Audit: &engine.AuditReport{CanAccept: true}})
	m = update(m, key("a"))
	if m.screen != screenAudit || len(f.sent) != 0 {
		t.Fatalf("a typed-ahead a accepted the report: screen %d, sent %+v", m.screen, f.sent)
	}
	advance(500 * time.Millisecond)
	update(m, key("a"))
	if len(f.sent) != 1 {
		t.Errorf("a deliberate a sent %+v, want the accept", f.sent)
	}
}

// q on the finished screen quits; Enter and Esc still go back to the menu.
func TestSummaryQuitsOnQ(t *testing.T) {
	m := fixedModel()
	m.screen = screenSummary
	if _, cmd := press(m, "q"); !quits(cmd) {
		t.Error("q on the summary did not quit")
	}
	for _, k := range []string{"enter", "esc"} {
		if got, cmd := press(m, k); got.screen != screenMenu || quits(cmd) {
			t.Errorf("%q on the summary: screen %d", k, got.screen)
		}
	}
}

// ctrl+c is never held and never renews the window: a password cannot contain
// it, so it is not typed-ahead. After a burst, a ctrl+c inside the window must
// leave the window to run out, so a deliberate y soon after still works.
func TestCtrlCDoesNotRenewTheWindow(t *testing.T) {
	advance := armedClock(t)
	m, f := engineModel()
	m = typeAhead(topgradeConfirm(m))
	advance(300 * time.Millisecond)
	m = update(m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if m.screen != screenConfirm || len(f.sent) != 0 {
		t.Fatalf("ctrl+c on the confirm: screen %d, sent %+v", m.screen, f.sent)
	}
	advance(200 * time.Millisecond) // 500 ms after the burst, 200 after ctrl+c
	update(m, key("y"))
	if got := lastSent(t, f); got != (engine.Confirm{Prompt: 3, Yes: true}) {
		t.Errorf("y after ctrl+c sent %#v: ctrl+c renewed the window", got)
	}
}
