package stub

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/engine/enginetest"
	"github.com/Enzord758/archmaid/internal/steps"
)

// playAnswers plays action of Catalog, answering its prompts in order with
// answers (yes past the end).
func playAnswers(t *testing.T, action string, answers ...how) []engine.Event {
	t.Helper()
	evs, _ := play(t, action, Catalog(t0), answers)
	enginetest.CheckContract(t, evs)
	return evs
}

func passwordPrompts(evs []engine.Event) []engine.NeedsPassword {
	var out []engine.NeedsPassword
	for _, ev := range evs {
		if np, ok := ev.(engine.NeedsPassword); ok {
			out = append(out, np)
		}
	}
	return out
}

func TestUncachedOpensWithPassword(t *testing.T) {
	evs := playAnswers(t, IDFullCleanupUncached, cancelled)
	np := passwordPrompts(evs)
	want := engine.NeedsPassword{ID: 1, Kind: engine.PasswordUpFront, Gate: "Proceed with full cleanup?", AttemptsLeft: 3}
	if len(np) != 1 || np[0] != want {
		t.Errorf("prompts %#v, want [%#v]", np, want)
	}
	for _, ev := range evs {
		if _, ok := ev.(engine.NeedsConfirm); ok {
			t.Errorf("a separate gate %#v: the password prompt is the gate", ev)
		}
	}
	if f := evs[len(evs)-2].(engine.Finished); f.Outcome != engine.OutcomeCancelled {
		t.Errorf("cancel ended %v", f.Outcome)
	}
}

func TestUncachedRight(t *testing.T) {
	evs := playAnswers(t, IDFullCleanupUncached)
	var seen []string
	for _, ev := range evs {
		switch ev := ev.(type) {
		case engine.AuthWaiting:
			seen = append(seen, fmt.Sprint("waiting ", ev.Kind))
		case engine.AuthResolved:
			seen = append(seen, fmt.Sprint("resolved ", ev.Granted))
		}
	}
	if got := strings.Join(seen, ","); got != "waiting 0,resolved true" {
		t.Errorf("auth events %q", got)
	}
	// It goes on like FullCleanup: the same steps, the gate aside.
	fc, _ := play(t, "fullcleanup", Catalog(t0), nil)
	if f := evs[len(evs)-2].(engine.Finished); f.Outcome != engine.OutcomeCompleted || f.Skipped != 0 {
		t.Errorf("ended %+v", f)
	}
	if a, b := countType[engine.StepDone](evs), countType[engine.StepDone](fc); a != b {
		t.Errorf("%d StepDone, FullCleanup has %d", a, b)
	}
}

func countType[T engine.Event](evs []engine.Event) int {
	n := 0
	for _, ev := range evs {
		if _, ok := ev.(T); ok {
			n++
		}
	}
	return n
}

func TestUncachedWrongOnce(t *testing.T) {
	evs := playAnswers(t, IDFullCleanupUncached, wrong, yes)
	var at int
	for i, ev := range evs {
		if np, ok := ev.(engine.NeedsPassword); ok && np.ID == 1 {
			at = i
		}
	}
	want := []engine.Event{
		engine.AuthWaiting{Kind: engine.WaitPasswordCheck},
		engine.AuthResolved{Granted: false, AttemptsLeft: 2},
		engine.NeedsPassword{ID: 2, Kind: engine.PasswordUpFront, Gate: "Proceed with full cleanup?", AttemptsLeft: 2,
			Error: "Incorrect password — 2 attempts left"},
		engine.AuthWaiting{Kind: engine.WaitPasswordCheck},
		engine.AuthResolved{Granted: true},
	}
	for i, w := range want {
		if got := evs[at+1+i]; got != w {
			t.Fatalf("event +%d: got %#v, want %#v", 1+i, got, w)
		}
	}
	if f := evs[len(evs)-2].(engine.Finished); f.Outcome != engine.OutcomeCompleted || f.Skipped != 0 {
		t.Errorf("ended %+v", f)
	}
	// Later prompts never reuse an ID.
	seen := map[engine.PromptID]bool{}
	for _, ev := range evs {
		if id := promptID(ev); id != 0 {
			if seen[id] {
				t.Errorf("prompt ID %d reused", id)
			}
			seen[id] = true
		}
	}
}

func TestUncachedThreeWrong(t *testing.T) {
	evs := playAnswers(t, IDFullCleanupUncached, wrong, wrong, wrong)
	np := passwordPrompts(evs)
	if len(np) != 3 || np[1].AttemptsLeft != 2 || np[2].AttemptsLeft != 1 ||
		np[2].Error != "Incorrect password — 1 attempt left" {
		t.Errorf("prompts %#v", np)
	}
	var resolved []engine.AuthResolved
	refused, logs := 0, 0
	for _, ev := range evs {
		switch ev := ev.(type) {
		case engine.AuthResolved:
			resolved = append(resolved, ev)
		case engine.StepDone:
			if ev.Status == steps.AuthRefused {
				refused++
			}
		case engine.NeedsConfirm:
			t.Errorf("a confirm %#v after sudo gave up", ev)
		case engine.LogWritten:
			if strings.Contains(ev.Header, "(sudo authentication failed)") {
				logs++
			}
		}
	}
	if len(resolved) != 3 || resolved[2] != (engine.AuthResolved{AttemptsLeft: 0}) {
		t.Errorf("AuthResolved %#v", resolved)
	}
	if refused != 6 || logs != 6 {
		t.Errorf("%d steps AuthRefused, %d logged, want 6 and 6", refused, logs)
	}
	if f := evs[len(evs)-2].(engine.Finished); f.Outcome != engine.OutcomeCompleted || f.Skipped != 6 {
		t.Errorf("ended %+v", f)
	}
}

func TestAskpassMidStep(t *testing.T) {
	want := engine.NeedsPassword{ID: 2, Kind: engine.PasswordAskpass, Step: steps.IDTopgrade,
		Requester: "yay (pid 4242)", Prompt: "[sudo] password for user:", AttemptsLeft: 3}
	for _, c := range []struct {
		name string
		h    how
		auth []engine.Event
	}{
		{"right", yes, []engine.Event{engine.AuthWaiting{Kind: engine.WaitPasswordCheck}, engine.AuthResolved{Granted: true}}},
		{"cancel", cancelled, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			evs := playAnswers(t, IDFullCleanupAskpass, yes, c.h)
			np := passwordPrompts(evs)
			if len(np) != 1 || np[0] != want {
				t.Fatalf("prompts %#v, want [%#v]", np, want)
			}
			var auth []engine.Event
			running := false
			for _, ev := range evs {
				switch ev := ev.(type) {
				case engine.StepStarted:
					running = ev.ID == steps.IDTopgrade
				case engine.StepDone:
					running = false
				case engine.NeedsPassword:
					if !running {
						t.Error("the askpass prompt is not inside the topgrade step")
					}
				case engine.AuthWaiting, engine.AuthResolved:
					auth = append(auth, ev)
				}
			}
			if fmt.Sprint(auth) != fmt.Sprint(c.auth) {
				t.Errorf("auth events %v, want %v", auth, c.auth)
			}
			// The step goes on to its own end and the run completes.
			var topgrade steps.Status = -1
			for _, ev := range evs {
				if sd, ok := ev.(engine.StepDone); ok && sd.ID == steps.IDTopgrade {
					topgrade = sd.Status
				}
			}
			if topgrade != steps.OK {
				t.Errorf("topgrade ended %v", topgrade)
			}
			if f := evs[len(evs)-2].(engine.Finished); f.Outcome != engine.OutcomeCompleted {
				t.Errorf("ended %+v", f)
			}
		})
	}
}

// An Abort at the askpass prompt withdraws it as the engine does.
func TestAskpassAbort(t *testing.T) {
	d := New(nil, Options{Clock: NewFakeClock(t0), Scripts: Catalog(t0)})
	defer closeDrained(t, d)
	send(t, d, engine.Start{Action: IDFullCleanupAskpass})
	var evs []engine.Event
	for n := 0; ; n++ {
		ev := next(t, d)
		evs = append(evs, ev)
		if id := promptID(ev); id != 0 {
			if _, ok := ev.(engine.NeedsPassword); ok {
				send(t, d, engine.Abort{})
			} else {
				send(t, d, engine.Confirm{Prompt: id, Yes: true})
			}
		}
		if _, ok := ev.(engine.Finished); ok {
			break
		}
	}
	enginetest.CheckContract(t, evs)
}

// No Event on any path carries a password: the one the tests send, as text
// anywhere in a printed Event, and no script holds a command (the engine's
// own reflection test covers the Event types).
func TestNoEventCarriesTheSecret(t *testing.T) {
	cat := Catalog(t0)
	for _, id := range []string{IDFullCleanupUncached, IDFullCleanupAskpass} {
		for _, p := range walk(t, id, cat) {
			for _, ev := range p.events {
				if s := fmt.Sprintf("%#v", ev); strings.Contains(s, string(rightSecret)) {
					t.Errorf("%s %s: %s carries the password", id, p, s)
				}
			}
		}
	}
	// The scripts themselves, branches included, hold neither secret.
	for id, s := range cat {
		for _, ev := range allCueEvents(s) {
			if p := fmt.Sprintf("%#v", ev); strings.Contains(p, string(rightSecret)) || strings.Contains(p, WrongPassword) {
				t.Errorf("%s: %s holds a password", id, p)
			}
		}
	}
}

// allCueEvents is every Event of s and of its branches.
func allCueEvents(s Script) []engine.Event {
	var out []engine.Event
	for _, c := range s {
		out = append(out, c.Event)
		out = append(out, allCueEvents(c.OnNo)...)
		out = append(out, allCueEvents(c.OnCancel)...)
		out = append(out, allCueEvents(c.OnWrong)...)
	}
	return out
}

func TestUncachedWalkPaths(t *testing.T) {
	// Password 1: right | no | cancel | wrong. Right: orphans 3 paths.
	// Wrong: right (3) | cancel x2 | wrong: right (3) | cancel x2 |
	// wrong (the third: gives up) — and so on, counted below.
	paths := walk(t, IDFullCleanupUncached, Catalog(t0))
	gave := 0
	for _, p := range paths {
		if p.finished().Skipped == 6 {
			gave++
		}
	}
	if gave != 1 {
		t.Errorf("%d paths ended in three wrong passwords, want 1", gave)
	}
	// 1st prompt: no, cancel (2 ends); right (3); wrong -> 2nd: no, cancel,
	// right (3), wrong -> 3rd: no, cancel, right (3), wrong (1 end).
	if want := 2 + 3 + (2 + 3 + (2 + 3 + 1)); len(paths) != want {
		t.Errorf("%d paths, want %d", len(paths), want)
	}
}

func TestCatalogUncached(t *testing.T) {
	c := CatalogUncached(t0)
	if len(c) != len(Catalog(t0)) {
		t.Error("CatalogUncached has other keys than Catalog")
	}
	evs, _ := play(t, "fullcleanup", c, []how{cancelled})
	if len(passwordPrompts(evs)) != 1 {
		t.Error("fullcleanup in CatalogUncached doesn't ask for the password")
	}
}
