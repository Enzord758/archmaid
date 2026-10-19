package stub

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/engine/enginetest"
	"github.com/Enzord758/archmaid/internal/steps"
)

// rightSecret is the password the tests send for a right one; no Event may
// carry it.
const rightSecret engine.Secret = "s3cret-hunter2"

// reply is how a test answers the prompt that is open.
func reply(id engine.PromptID, ev engine.Event, h how) engine.Command {
	switch h {
	case cancelled:
		return engine.CancelPrompt{Prompt: id}
	case wrong:
		return engine.SubmitPassword{Prompt: id, Password: WrongPassword}
	case no:
		if _, ok := ev.(engine.NeedsSelection); ok {
			return engine.Select{Prompt: id}
		}
		return engine.Confirm{Prompt: id, Yes: false}
	}
	switch ev := ev.(type) {
	case engine.NeedsSelection:
		return engine.Select{Prompt: id, Picked: ev.Items[:1]}
	case engine.NeedsPassword:
		return engine.SubmitPassword{Prompt: id, Password: rightSecret}
	}
	return engine.Confirm{Prompt: id, Yes: true}
}

// play runs one whole action of script on a fresh Driver, answering its
// n-th prompt with answers[n] (yes past the end), and returns the events
// and how many prompts it opened.
func play(t *testing.T, action string, scripts map[string]Script, answers []how) ([]engine.Event, int) {
	t.Helper()
	d := New(nil, Options{Clock: NewFakeClock(t0), Scripts: scripts})
	defer closeDrained(t, d)
	send(t, d, engine.Start{Action: action})
	var evs []engine.Event
	prompts := 0
	for {
		ev := next(t, d)
		evs = append(evs, ev)
		if id := promptID(ev); id != 0 {
			h := yes
			if prompts < len(answers) {
				h = answers[prompts]
			}
			prompts++
			send(t, d, reply(id, ev, h))
		}
		if _, ok := ev.(engine.PhaseChanged); ok && len(evs) > 1 {
			if _, fin := evs[len(evs)-2].(engine.Finished); fin {
				return evs, prompts
			}
		}
	}
}

// path is one walked answer sequence and where it ended.
type path struct {
	answers []how
	events  []engine.Event
}

func (p path) finished() engine.Finished {
	for _, ev := range p.events {
		if f, ok := ev.(engine.Finished); ok {
			return f
		}
	}
	return engine.Finished{Outcome: -1}
}

func (p path) String() string {
	var s []string
	for _, h := range p.answers {
		s = append(s, [...]string{"yes", "no", "cancel", "wrong"}[h])
	}
	return "[" + strings.Join(s, " ") + "]"
}

// walk plays action down every answer sequence its prompts allow (each one
// answered yes, no or cancel, a branch's own prompts included), holding
// every run to the contract.
func walk(t *testing.T, action string, scripts map[string]Script) []path {
	t.Helper()
	var out []path
	var explore func(prefix []how)
	explore = func(prefix []how) {
		evs, n := play(t, action, scripts, prefix)
		ans := make([]how, n)
		for i := range ans {
			ans[i] = yes
			if i < len(prefix) {
				ans[i] = prefix[i]
			}
		}
		p := path{answers: ans, events: evs}
		out = append(out, p)
		enginetest.CheckContract(t, evs)
		if last := evs[len(evs)-1]; last != (engine.PhaseChanged{Phase: engine.PhaseIdle}) {
			t.Errorf("%s: run ends in %#v, not PhaseChanged idle", p, last)
		}
		var prompts []engine.Event
		for _, ev := range evs {
			if promptID(ev) != 0 {
				prompts = append(prompts, ev)
			}
		}
		for i := len(prefix); i < n; i++ {
			alts := []how{no, cancelled}
			if _, ok := prompts[i].(engine.NeedsPassword); ok {
				alts = append(alts, wrong)
			}
			for _, alt := range alts {
				next := slices.Clone(ans[:i])
				explore(append(next, alt))
			}
		}
	}
	explore(nil)
	return out
}

// Every ready-made script holds to the contract on every branch, and the
// branches end where their docs say.
func TestCatalogEveryBranch(t *testing.T) {
	cat := Catalog(t0)
	type end struct {
		outcome engine.Outcome
		skipped int
	}
	want := map[string]map[end]bool{
		actions.IDFullCleanup: {
			{engine.OutcomeCompleted, 0}: true,
			{engine.OutcomeCompleted, 1}: true,
			{engine.OutcomeCancelled, 0}: true,
		},
		IDFullCleanupUncached: {
			{engine.OutcomeCompleted, 0}: true,
			{engine.OutcomeCompleted, 1}: true,
			{engine.OutcomeCompleted, 6}: true, // three wrong passwords
			{engine.OutcomeCancelled, 0}: true,
		},
		IDFullCleanupAskpass: {
			{engine.OutcomeCompleted, 0}: true,
			{engine.OutcomeCompleted, 1}: true,
			{engine.OutcomeCancelled, 0}: true,
		},
		actions.IDClearCaches: {{engine.OutcomeCompleted, 0}: true, {engine.OutcomeCancelled, 0}: true},
		actions.IDServices:    {{engine.OutcomeCompleted, 0}: true, {engine.OutcomeCancelled, 0}: true},
		actions.IDAURAudit:    {{engine.OutcomeReport, 0}: true},
		actions.IDRepair:      {{engine.OutcomeNotice, 0}: true},
		IDClearCachesNone:     {{engine.OutcomeNotice, 0}: true},
	}
	for id := range cat {
		if want[id] == nil {
			t.Errorf("Catalog has %q, which the table doesn't know", id)
		}
	}
	for id, ends := range want {
		t.Run(id, func(t *testing.T) {
			if _, ok := cat[id]; !ok {
				t.Fatalf("Catalog has no %q", id)
			}
			got := map[end]bool{}
			for _, p := range walk(t, id, cat) {
				f := p.finished()
				if f.Action != strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(id, "-uncached"), "-askpass"), "-none") {
					t.Errorf("%s: Finished.Action = %q", p, f.Action)
				}
				e := end{f.Outcome, f.Skipped}
				if !ends[e] {
					t.Errorf("%s: ended %v with %d skipped, which isn't a listed ending", p, f.Outcome, f.Skipped)
				}
				got[e] = true
			}
			if !reflect.DeepEqual(got, ends) {
				t.Errorf("endings reached %v, want %v", got, ends)
			}
		})
	}
}

func TestFullCleanupBranchesWalk(t *testing.T) {
	paths := walk(t, "fullcleanup", map[string]Script{"fullcleanup": FullCleanupBranches(t0)})
	// gate: yes|no|cancel; orphans (behind a yes gate) likewise. There is no
	// cache-wipe confirm in Full Cleanup (task #186).
	// gate yes: orphans 3 ways = 3; gate no, cancel = 2.
	if len(paths) != 5 {
		t.Errorf("%d paths, want 5", len(paths))
	}
	for _, p := range paths {
		skipped := 0
		for _, ev := range p.events {
			if sd, ok := ev.(engine.StepDone); ok && sd.Status == steps.Skipped {
				skipped++
			}
		}
		if f := p.finished(); f.Skipped != skipped {
			t.Errorf("%s: Finished.Skipped = %d, but %d steps were skipped", p, f.Skipped, skipped)
		}
	}
}

func TestBranchesAreAdditive(t *testing.T) {
	plain, branched := FullCleanup(t0), FullCleanupBranches(t0)
	if !reflect.DeepEqual(plain.Events(), branched.Events()) {
		t.Error("the branching script's yes path differs from FullCleanup")
	}
	for i, c := range plain {
		if c.OnNo != nil || c.OnCancel != nil {
			t.Errorf("FullCleanup cue %d carries a branch", i)
		}
	}
	if branched[1].OnNo == nil {
		t.Error("the gate has no OnNo")
	}
}

func TestUnknownAction(t *testing.T) {
	d := New(nil, Options{Clock: NewFakeClock(t0), Scripts: map[string]Script{"x": {}}})
	defer closeDrained(t, d)
	send(t, d, engine.Start{Action: "nope"})
	var got []engine.Event
	for range 4 {
		got = append(got, next(t, d))
	}
	if r, ok := got[0].(engine.DiscoveryResult); !ok || r.Outcome != engine.DiscoveryFailed || !strings.Contains(r.Summary, "nope") {
		t.Errorf("first event %#v, want a failed DiscoveryResult naming the action", got[0])
	}
	if n, ok := got[1].(engine.Notice); !ok || !strings.Contains(n.Text, "nope") {
		t.Errorf("second event %#v, want a Notice naming the action", got[1])
	}
	if got[2] != (engine.Finished{Action: "nope", Outcome: engine.OutcomeNotice}) {
		t.Errorf("third event %#v", got[2])
	}
	enginetest.CheckContract(t, got)
	// The Driver is idle again and takes the next Start.
	send(t, d, engine.Start{Action: "x"})
}

// OnCancel wins over OnNo for a cancel; without it a cancel takes OnNo; a
// branch may hold prompts of its own, and an Abort inside one ends the run.
func TestBranchSelection(t *testing.T) {
	fin := func(msg string) Script {
		return Script{{Event: engine.Notice{Text: msg}}, {Event: engine.Finished{Action: "x", Outcome: engine.OutcomeNotice}}}
	}
	script := Script{
		{Event: engine.NeedsConfirm{ID: 1, Msg: "q?"}, OnNo: Script{
			{Event: engine.NeedsConfirm{ID: 2, Msg: "sure?"}, OnNo: fin("really no")},
			{Event: engine.Notice{Text: "no, then yes"}},
			{Event: engine.Finished{Action: "x", Outcome: engine.OutcomeNotice}},
		}, OnCancel: fin("cancelled")},
		{Event: engine.Notice{Text: "yes"}},
		{Event: engine.Finished{Action: "x", Outcome: engine.OutcomeNotice}},
	}
	run := func(cmds ...engine.Command) []string {
		d := New(script, Options{Clock: NewFakeClock(t0)})
		defer closeDrained(t, d)
		send(t, d, engine.Start{Action: "x"})
		var notes []string
		for _, c := range cmds {
			for {
				ev := next(t, d)
				if n, ok := ev.(engine.Notice); ok {
					notes = append(notes, n.Text)
				}
				if promptID(ev) != 0 {
					break
				}
			}
			send(t, d, c)
		}
		for {
			ev := next(t, d)
			if n, ok := ev.(engine.Notice); ok {
				notes = append(notes, n.Text)
			}
			if _, ok := ev.(engine.Finished); ok {
				return notes
			}
		}
	}
	for _, c := range []struct {
		name string
		cmds []engine.Command
		want []string
	}{
		{"yes", []engine.Command{engine.Confirm{Prompt: 1, Yes: true}}, []string{"yes"}},
		{"cancel", []engine.Command{engine.CancelPrompt{Prompt: 1}}, []string{"cancelled"}},
		{"no then yes", []engine.Command{engine.Confirm{Prompt: 1}, engine.Confirm{Prompt: 2, Yes: true}}, []string{"no, then yes"}},
		{"no then no", []engine.Command{engine.Confirm{Prompt: 1}, engine.Confirm{Prompt: 2}}, []string{"really no"}},
		{"no then cancel", []engine.Command{engine.Confirm{Prompt: 1}, engine.CancelPrompt{Prompt: 2}}, []string{"really no"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := run(c.cmds...); !reflect.DeepEqual(got, c.want) {
				t.Errorf("notes %v, want %v", got, c.want)
			}
		})
	}

	d := New(script, Options{Clock: NewFakeClock(t0)})
	defer closeDrained(t, d)
	send(t, d, engine.Start{Action: "x"})
	expect(t, d, engine.NeedsConfirm{ID: 1, Msg: "q?"})
	send(t, d, engine.Confirm{Prompt: 1})
	expect(t, d, engine.NeedsConfirm{ID: 2, Msg: "sure?"})
	send(t, d, engine.Abort{})
	expect(t, d,
		engine.PromptClosed{ID: 2},
		engine.Finished{Action: "x", Outcome: engine.OutcomeCancelled},
		engine.PhaseChanged{Phase: engine.PhaseIdle})
}

// Close while a branch plays returns.
func TestCloseInBranch(t *testing.T) {
	d := New(Script{{Event: engine.NeedsConfirm{ID: 1}, OnNo: Script{{Event: engine.Notice{Text: "n"}}}}}, Options{Clock: NewFakeClock(t0)})
	send(t, d, engine.Start{Action: "x"})
	expect(t, d, engine.NeedsConfirm{ID: 1})
	send(t, d, engine.Confirm{Prompt: 1})
	closeDrained(t, d) // the branch's Notice is never read
}
