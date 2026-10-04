package testtranscript

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// fakeTarget hands out a fixed event sequence; a reply appends what it led to.
type fakeTarget struct {
	label   string
	events  []Event
	onReply func(Answer) []Event
	replies []Answer
	out     Outcome
	err     error // returned by Next once the events run out
}

func (f *fakeTarget) Start(string) (string, error) { return f.label, nil }

func (f *fakeTarget) Next() (Event, error) {
	if len(f.events) == 0 {
		return nil, f.err
	}
	ev := f.events[0]
	f.events = f.events[1:]
	return ev, nil
}

func (f *fakeTarget) Reply(a Answer) error {
	f.replies = append(f.replies, a)
	if f.onReply != nil {
		f.events = append(f.events, f.onReply(a)...)
	}
	return nil
}

func (f *fakeTarget) Outcome() (Outcome, error) { return f.out, nil }

var (
	auditStep = Step{Title: "Auditing AUR packages…", InProc: true, AlwaysLog: true, Category: "audit"}
	topgrade  = Step{Title: "Updating system with topgrade…", Argv: []string{"topgrade", "-y"}, Sudo: true, AlwaysLog: true, Category: "topgrade"}
	probe     = Step{Title: "Finding orphaned packages…", InProc: true, Category: "orphans"}
	removal   = Step{Title: "Removing orphaned packages…", Argv: []string{"sudo", "pacman", "-Rns", "--", "libfoo"},
		Sudo: true, Confirm: true, Optional: true, AlwaysLog: true, Category: "orphans"}
)

// TestRecorderFormatsEvents drives the Recorder with a hand-built run that
// uses every event kind, and checks the lines it writes: the transcript
// format belongs to the Recorder, whichever Target produced the events.
func TestRecorderFormatsEvents(t *testing.T) {
	gated := topgrade
	gated.Confirm = true
	tg := &fakeTarget{
		label: "Full Cleanup (Recommended)",
		events: []Event{
			Exec{Cmdline: "pacman -Qqm"},
			NeedsPassword{},
		},
		out: Outcome{Kind: "aborted", Errs: 1, Skipped: 2},
	}
	tg.onReply = func(a Answer) []Event {
		switch a.Kind {
		case "password":
			return []Event{Exec{Cmdline: "su -s /usr/bin/true alice"}, NeedsConfirm{Text: "Proceed?"}}
		case "confirm":
			return []Event{
				QueueStarted{Steps: []Step{auditStep, topgrade, probe}},
				StepStarted{Index: 0},
				OutputLine{Index: 0, Text: "report in /home/me/.local/state/archmaid/audit.log"},
				OutputLine{Index: 0, Text: "saved under /home/me"},
				OutputLine{Index: 0, Text: ""},
				StepDone{Index: 0, Status: OK},
				PlanChanged{Index: 1, Step: gated},
				LogWritten{Category: "audit", Header: "OK: AUR audit"},
				SnapshotChanged{Entries: []SnapEntry{
					{Name: "yay-bin", Maintainer: "mallory", HasCo: true},
					{Name: "foo-git", Maintainer: "eve", HasCo: true, CoMaintainers: []string{"a", "b"}},
					{Name: "old", Maintainer: ""},
				}},
				StepStarted{Index: 1},
				StepDone{Index: 1, Status: Failed},
				StepDone{Index: 2, Status: Skipped},
				StepInserted{Index: 3, Step: removal},
				NeedsSelection{Options: []string{"a.service", "b.service"}},
			}
		case "select":
			return []Event{StepStarted{Index: 3}}
		case "abort":
			return []Event{
				Exec{Cmdline: "sudo -A pacman -Rns -- libfoo"},
				Aborted{Index: 3},
				SnapshotChanged{Unreadable: true},
				Notice{Text: "Aborted."},
				OutputReport{Text: "2 checked\n\n✖ yay-bin"},
			}
		}
		return nil
	}
	r := NewRecorder(t, []Answer{
		{"password", "", "correct"},
		{"confirm", "Proceed", "yes"},
		{"select", "", "b.service"},
		{"abort", "orphaned", ""},
	}, "/home/me", "$HOME", "/home/me/.local/state/archmaid", "$LOGDIR", "", "ignored")
	r.Run(tg, "Full Cleanup")

	want := `action "Full Cleanup (Recommended)"
exec  pacman -Qqm
auth  password -> <correct>
exec  su -s /usr/bin/true $USER
ask   "Proceed?" -> yes
plan  3 steps
plan  1 "Auditing AUR packages…" inproc alwayslog cat=audit
plan  2 "Updating system with topgrade…" argv=topgrade -y sudo alwayslog cat=topgrade
plan  3 "Finding orphaned packages…" inproc cat=orphans
step  1 running
out   1 report in $LOGDIR/audit.log
out   1 saved under $HOME
out   1
step  1 ok
plan  2 "Updating system with topgrade…" argv=topgrade -y sudo confirm alwayslog cat=topgrade
log   audit OK: AUR audit
snap  foo-git="eve"+co[a,b] old=""+co? yay-bin="mallory"
step  2 running
step  2 failed
step  3 skipped
plan  4 steps
plan  +4 "Removing orphaned packages…" argv=sudo pacman -Rns -- libfoo sudo confirm optional alwayslog cat=orphans
pick  [a.service b.service] -> [b.service]
step  4 running
exec  sudo -A pacman -Rns -- libfoo
abort 4 "Removing orphaned packages…"
snap  unreadable
note  "Aborted."
audit 2 checked
audit
audit ✖ yay-bin
end   outcome=aborted errs=1 skipped=2
`
	if got := r.Transcript(); got != want {
		t.Fatalf("transcript:\n%s\nwant:\n%s", got, want)
	}
	if len(tg.replies) != 4 || tg.replies[3].Kind != "abort" {
		t.Fatalf("replies %+v", tg.replies)
	}
}

// fakeTB records failures; Fatalf unwinds with errFatal like FailNow would.
type fakeTB struct {
	testing.TB
	msgs []string
}

var errFatal = errors.New("fatal")

func (f *fakeTB) Helper() {}
func (f *fakeTB) Errorf(format string, a ...any) {
	f.msgs = append(f.msgs, fmt.Sprintf(format, a...))
}
func (f *fakeTB) Fatalf(format string, a ...any) {
	f.Errorf(format, a...)
	panic(errFatal)
}

// record runs events on a fake target under a fake TB and returns what failed.
func record(script []Answer, events ...Event) (msgs []string, r *Recorder) {
	tb := &fakeTB{}
	defer func() {
		if p := recover(); p != nil && p != errFatal {
			panic(p)
		}
		msgs = tb.msgs
	}()
	r = NewRecorder(tb, script)
	r.Run(&fakeTarget{label: "X", events: events, out: Outcome{Kind: "completed"}}, "X")
	return tb.msgs, r
}

// TestRecorderScriptFaults: a prompt the script doesn't expect, a reply that
// isn't yes or no, an unused answer, a step outside the plan and a target
// error all fail the test, with the transcript so far.
func TestRecorderScriptFaults(t *testing.T) {
	for _, c := range []struct {
		name   string
		script []Answer
		events []Event
		want   string
	}{
		{"unscripted", nil, []Event{Exec{Cmdline: "a"}, NeedsConfirm{Text: "Go?"}}, "unscripted confirm prompt \"Go?\"\naction \"X\"\nexec  a"},
		{"wrong kind", []Answer{{"password", "", "correct"}}, []Event{NeedsConfirm{Text: "Go?"}}, `script wants password "", got confirm prompt "Go?"`},
		{"wrong text", []Answer{{"confirm", "Stop", "yes"}}, []Event{NeedsConfirm{Text: "Go?"}}, `script wants confirm "Stop"`},
		{"bad reply", []Answer{{"confirm", "", "maybe"}}, []Event{NeedsConfirm{Text: "Go?"}}, `confirm reply "maybe"`},
		{"unused", []Answer{{"abort", "x", ""}}, nil, "unused script answers"},
		{"no plan", nil, []Event{StepStarted{Index: 0}}, "step index 0 outside the 0-step plan"},
		{"not done", nil, []Event{QueueStarted{Steps: []Step{probe}}, StepDone{Index: 0, Status: Running}}, "step 1 done with status running"},
	} {
		t.Run(c.name, func(t *testing.T) {
			msgs, _ := record(c.script, c.events...)
			if len(msgs) != 1 || !strings.Contains(msgs[0], c.want) {
				t.Fatalf("failures %q, want one containing %q", msgs, c.want)
			}
		})
	}

	t.Run("target error", func(t *testing.T) {
		tb := &fakeTB{}
		func() {
			defer func() { _ = recover() }()
			NewRecorder(tb, nil).Run(&fakeTarget{label: "X", events: []Event{Exec{Cmdline: "a"}}, err: errors.New("stalled")}, "X")
		}()
		if len(tb.msgs) != 1 || tb.msgs[0] != "stalled\naction \"X\"\nexec  a" {
			t.Fatalf("failures %q", tb.msgs)
		}
	})
}

func TestStatusString(t *testing.T) {
	for s, want := range map[Status]string{Pending: "pending", Running: "running", OK: "ok", Failed: "failed", Skipped: "skipped", Skipped + 1: "status?"} {
		if got := s.String(); got != want {
			t.Errorf("%d: %q, want %q", s, got, want)
		}
	}
}

func TestSameFlagsIgnoresTitle(t *testing.T) {
	renamed := topgrade
	renamed.Title = "Upgrading…"
	if !topgrade.SameFlags(renamed) {
		t.Fatal("a new title alone is not a flag change")
	}
	for _, change := range []func(*Step){
		func(s *Step) { s.InProc = true },
		func(s *Step) { s.Argv = []string{"topgrade"} },
		func(s *Step) { s.Sudo = false },
		func(s *Step) { s.Confirm = true },
		func(s *Step) { s.Optional = true },
		func(s *Step) { s.AlwaysLog = false },
		func(s *Step) { s.Category = "cleanup" },
	} {
		s := topgrade
		s.Argv = append([]string(nil), topgrade.Argv...)
		change(&s)
		if topgrade.SameFlags(s) {
			t.Errorf("%+v reported unchanged", s)
		}
	}
}
