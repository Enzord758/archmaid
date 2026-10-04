package main

// realDriverTarget: the transcript Target that runs every scenario through
// the real internal/driver (driver.New(engine.New(opts), deps)), with the
// Deps the TUI will wire in E7b (tuiDriverDeps), reading only its Events
// channel. The Driver runs its Effects on goroutines of its own, so where
// a program's exec line, a log entry or a snapshot write lands among the
// Events isn't behaviour; the goldens are compared under a projection
// (checkGoldenProjected): every other line exactly and in order, the exec
// lines in order (runs of one command sorted, as effects.execs does), the
// log lines in order per category and the snapshot lines in order.

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/driver"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/engine/enginetest"
	"github.com/Enzord758/archmaid/internal/sysstat"

	tt "github.com/Enzord758/archmaid/internal/testtranscript"
)

// TestTranscriptRealDriver replays every scenario through the real Driver.
func TestTranscriptRealDriver(t *testing.T) {
	for _, sc := range transcriptScenarios {
		t.Run(sc.name, func(t *testing.T) {
			if *updateTranscripts {
				t.Skip("-update-transcripts records through the TUI (TestTranscript) only")
			}
			var tg *realDriverTarget
			got := sc.run(t, func(t *testing.T, shimLog string) tt.Target {
				tg = newRealDriverTarget(t, shimLog)
				return tg
			}).Transcript()
			checkGoldenProjected(t, sc.name, got)
			tg.finish(t)
		})
	}
}

// eventWait bounds the wait for the Driver's next Event: no step in the
// scenarios takes more than a fraction of it.
const eventWait = 10 * time.Second

type realDriverTarget struct {
	d        *driver.Driver
	fx       *effects
	contract *enginetest.Checker

	pending []tt.Event
	err     error
	flags   []tt.Step // the plan as last recorded

	execs      []string       // every exec line taken so far
	launched   map[string]int // child steps started, by command line
	running    int            // the running step's index, -1 for none
	engineLogs []tt.Event     // the LogWritten Events, as reported
	fileLogs   []tt.Event     // the entries the log files got
	prompt     tt.Event       // the open prompt, or nil
	promptID   engine.PromptID
	fin        *engine.Finished

	mu      sync.Mutex
	reports []error // what the Driver told Deps.Report
}

func newRealDriverTarget(t *testing.T, shimLog string) *realDriverTarget {
	t.Helper()
	// The askpass Server, as the TUI starts it (under XDG_RUNTIME_DIR, here
	// a short scratch dir: a socket path has ~108 bytes). Its wrapper is
	// only named in sudo's environment, never run: sudo is a shim.
	rt, err := os.MkdirTemp("", "rdrv")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(rt) })
	t.Setenv("XDG_RUNTIME_DIR", rt)
	oldWrapper := askpassWrapper
	srv := startAskpassServer()
	if srv == nil {
		t.Fatal("the askpass Server didn't start")
	}
	if !strings.HasPrefix(srv.Wrapper(), rt+string(filepath.Separator)) {
		t.Fatalf("the askpass wrapper %q is outside the scratch runtime dir", srv.Wrapper())
	}

	opts := engine.Options{DryRun: dryRun, Env: actionEnv()}
	if err := opts.Validate(); err != nil {
		t.Fatal(err)
	}
	tg := &realDriverTarget{fx: newEffects(shimLog), contract: enginetest.NewChecker(t),
		launched: map[string]int{}, running: -1}
	deps := tuiDriverDeps(srv)
	// Let the prefetch finish before the Driver goes on, so its pacman -Qqm
	// lands before the action's own queries, as sessionTarget does.
	deps.Prefetch = func(name string) driver.Prefetch {
		p := startPrefetch(name)
		if p != nil {
			select {
			case <-p.Done():
			case <-time.After(5 * time.Second):
				tg.report(fmt.Errorf("the prefetched AUR audit never finished"))
			}
		}
		return p
	}
	// Never the real tally: the shimmed su keeps no count.
	deps.Faillock = func() engine.FaillockRead { return engine.FaillockRead{Deny: 3} }
	// The status line is pinned, as in every transcript.
	deps.Status = func() sysstat.Status { return sysstat.Status{Free: "100G", RAM: "8G", Cache: "512M"} }
	deps.Report = tg.report
	tg.d = driver.New(engine.New(opts), deps)
	t.Cleanup(func() {
		tg.d.Close()
		stopAskpassServer(srv)
		askpassWrapper = oldWrapper
	})
	return tg
}

func (tg *realDriverTarget) report(err error) {
	tg.mu.Lock()
	defer tg.mu.Unlock()
	tg.reports = append(tg.reports, err)
}

// finish checks what the recording can't see: the engine's contract held
// to the end, every LogWritten reached its file, and the Driver reported
// nothing but a hook's own error.
func (tg *realDriverTarget) finish(t *testing.T) {
	t.Helper()
	if t.Failed() {
		return
	}
	tg.contract.Done()
	if !contains(tg.fileLogs, tg.engineLogs) {
		t.Errorf("the driver reported log entries %v, the log files got %v", tg.engineLogs, tg.fileLogs)
	}
	tg.mu.Lock()
	defer tg.mu.Unlock()
	for _, err := range tg.reports {
		if strings.Contains(err.Error(), "driver: hook of step") {
			t.Logf("Driver report: %v", err)
			continue
		}
		t.Errorf("Driver report: %v", err)
	}
}

func (tg *realDriverTarget) push(evs ...tt.Event) { tg.pending = append(tg.pending, evs...) }

func (tg *realDriverTarget) fail(format string, a ...any) {
	if tg.err == nil {
		tg.err = fmt.Errorf(format, a...)
	}
}

// sideEffects takes the exec lines, log entries and snapshot writes since
// the last call.
func (tg *realDriverTarget) sideEffects() {
	for _, ev := range tg.fx.execs() {
		tg.execs = append(tg.execs, ev.(tt.Exec).Cmdline)
		tg.push(ev)
	}
	logs := tg.fx.logs()
	tg.fileLogs = append(tg.fileLogs, logs...)
	tg.push(logs...)
	tg.push(tg.fx.snapshot()...)
}

func (tg *realDriverTarget) send(c engine.Command) {
	if !tg.d.Send(c) {
		tg.fail("the driver refused %T", c)
	}
}

func (tg *realDriverTarget) Start(sub string) (string, error) {
	i := slices.IndexFunc(actions.Menu, func(a actions.Action) bool { return strings.Contains(a.Label, sub) })
	if i < 0 {
		return "", fmt.Errorf("no menu item %q", sub)
	}
	tg.send(engine.Start{Action: actions.Menu[i].ID})
	return actions.Menu[i].Label, nil
}

// Next hands out the next line's event, reading the Driver's Events until
// there is one; an open prompt once nothing is buffered, nil once the
// action has finished.
func (tg *realDriverTarget) Next() (tt.Event, error) {
	timeout := time.NewTimer(eventWait)
	defer timeout.Stop()
	for len(tg.pending) == 0 {
		switch {
		case tg.err != nil:
			return nil, tg.err
		case tg.prompt != nil:
			return tg.prompt, nil
		case tg.fin != nil:
			tg.sideEffects()
			if len(tg.pending) == 0 {
				return nil, nil
			}
			continue
		}
		select {
		case ev, ok := <-tg.d.Events():
			if !ok {
				return nil, fmt.Errorf("the Driver closed its Events before Finished")
			}
			tg.take(ev)
		case <-timeout.C:
			return nil, fmt.Errorf("stalled: no Event for %v, no prompt and no Finished", eventWait)
		}
	}
	ev := tg.pending[0]
	tg.pending = tg.pending[1:]
	return ev, nil
}

// take turns one Event into transcript events, after the side effects
// that came before it.
func (tg *realDriverTarget) take(ev engine.Event) {
	if !tg.contract.Check([]engine.Event{ev}) {
		tg.fail("%T breaks the engine's contract (see above)", ev)
	}
	tg.sideEffects()
	switch ev := ev.(type) {
	case engine.QueueStarted:
		ps := make([]tt.Step, len(ev.Steps))
		for i, s := range ev.Steps {
			ps[i] = transcriptStep(s)
		}
		tg.flags = slices.Clone(ps)
		tg.push(tt.QueueStarted{Steps: ps})
	case engine.OutputChunk:
		if ev.Dropped > 0 {
			tg.fail("the Driver dropped %d lines of step %d", ev.Dropped, ev.Index+1)
		}
		for _, l := range ev.Lines {
			tg.push(tt.OutputLine{Index: ev.Index, Text: l})
		}
	case engine.StepDone:
		tg.running = -1
		tg.push(tt.StepDone{Index: ev.Index, Status: transcriptStatus(ev.Status)})
	case engine.Aborted:
		tg.running = -1
		tg.push(tt.Aborted{Index: ev.Index})
	case engine.StepInserted:
		tg.flags = slices.Insert(tg.flags, ev.Index, transcriptStep(ev.Step))
		tg.push(tt.StepInserted{Index: ev.Index, Step: transcriptStep(ev.Step)})
	case engine.PlanChanged:
		if s := transcriptStep(ev.Step); !s.SameFlags(tg.flags[ev.Index]) {
			tg.flags[ev.Index] = s
			tg.push(tt.PlanChanged{Index: ev.Index, Step: s})
		}
	case engine.StepStarted:
		tg.running = ev.Index
		if s := tg.flags[ev.Index]; !s.InProc {
			tg.launched[strings.Join(s.Argv, " ")]++
		}
		tg.push(tt.StepStarted{Index: ev.Index})
	case engine.LogWritten:
		tg.engineLogs = append(tg.engineLogs, tt.LogWritten{Category: string(ev.Category), Header: ev.Header})
	case engine.Notice:
		tg.push(tt.Notice{Text: ev.Text})
	case engine.OutputReport:
		// The recorder ends on the report, as the TUI's audit screen:
		// dismiss it to get the action's Finished.
		tg.push(tt.OutputReport{Text: ev.Text})
		tg.send(engine.Confirm{Prompt: ev.ID, Yes: false})
	case engine.NeedsConfirm:
		tg.open(ev.ID, tt.NeedsConfirm{Text: ev.Msg})
	case engine.NeedsPassword:
		tg.open(ev.ID, tt.NeedsPassword{})
	case engine.NeedsSelection:
		tg.open(ev.ID, tt.NeedsSelection{Options: slices.Clone(ev.Items)})
	case engine.PromptClosed:
		if ev.ID == tg.promptID {
			tg.prompt, tg.promptID = nil, 0
		}
	case engine.Finished:
		tg.fin = &ev
	case engine.PhaseChanged, engine.StatusUpdated, engine.AuthWaiting, engine.AuthResolved,
		engine.FaillockState: // no transcript line
	default:
		tg.fail("event %T (%+v): teach the real driver target", ev, ev)
	}
}

func (tg *realDriverTarget) open(id engine.PromptID, ev tt.Event) {
	if tg.prompt != nil {
		tg.fail("prompt %v opened while %v is open", ev, tg.prompt)
	}
	tg.prompt, tg.promptID = ev, id
}

func (tg *realDriverTarget) Reply(a tt.Answer) error {
	id := tg.promptID
	switch a.Kind {
	case "confirm":
		tg.prompt, tg.promptID = nil, 0
		tg.send(engine.Confirm{Prompt: id, Yes: a.Reply == "yes"})
	case "password":
		pw := engine.Secret("not-the-password")
		if a.Reply == "correct" {
			pw = shimPassword
		}
		tg.prompt, tg.promptID = nil, 0
		tg.send(engine.SubmitPassword{Prompt: id, Password: pw})
	case "select":
		tg.prompt, tg.promptID = nil, 0
		tg.send(engine.Select{Prompt: id, Picked: strings.Fields(a.Reply)})
	case "abort":
		if err := tg.awaitChild(); err != nil {
			return err
		}
		tg.send(engine.Abort{})
	default:
		return fmt.Errorf("the real driver target can't reply %q", a.Kind)
	}
	return nil
}

// awaitChild waits, before an abort, until the running step's child has
// logged itself to its shim, so the shim's SIGINT trap (if any) is
// installed, as tuiTarget's abort does. The child may have logged before
// its StepStarted arrived (the Driver starts it first), so it counts the
// exec lines of the step's command line (sudo's -A or -n dropped) until
// there is one for each such step started.
func (tg *realDriverTarget) awaitChild() error {
	i := tg.running
	if i < 0 || i >= len(tg.flags) {
		return fmt.Errorf("abort with no running step")
	}
	s := tg.flags[i]
	if s.InProc || len(s.Argv) == 0 {
		return nil
	}
	cmdline := strings.Join(s.Argv, " ")
	deadline := time.Now().Add(5 * time.Second)
	for {
		n := 0
		for _, l := range tg.execs {
			f := strings.Fields(l)
			if len(f) > 1 && f[0] == "sudo" && (f[1] == "-A" || f[1] == "-n") {
				f = slices.Delete(f, 1, 2)
			}
			if strings.Join(f, " ") == cmdline {
				n++
			}
		}
		if n >= tg.launched[cmdline] {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("step %d's child never reached its shim", i+1)
		}
		time.Sleep(time.Millisecond)
		tg.sideEffects()
	}
}

func (tg *realDriverTarget) Outcome() (tt.Outcome, error) {
	if tg.fin == nil {
		return tt.Outcome{}, fmt.Errorf("the action never finished")
	}
	return tt.Outcome{Kind: tg.fin.Outcome.String(), Errs: tg.fin.Errs, Skipped: tg.fin.Skipped}, nil
}

// ── the projection ───────────────────────────────────────────────────────────

// projection is a transcript as the real Driver is held to it: the exec,
// log and snapshot lines apart from the rest.
type projection struct {
	other, exec, snap []string
	logs              map[string][]string // by category
}

// project splits a transcript's lines by kind. The exec lines' runs of
// one command and first argument are sorted across the whole run (as
// effects.execs sorts each chunk it reads): those processes start
// concurrently, so their order isn't behaviour.
func project(transcript string) projection {
	p := projection{logs: map[string][]string{}}
	for _, l := range strings.Split(strings.TrimSuffix(transcript, "\n"), "\n") {
		f := strings.Fields(l)
		kind := ""
		if len(f) > 0 {
			kind = f[0]
		}
		switch kind {
		case "exec":
			p.exec = append(p.exec, l)
		case "log":
			cat := ""
			if len(f) > 1 {
				cat = f[1]
			}
			p.logs[cat] = append(p.logs[cat], l)
		case "snap":
			p.snap = append(p.snap, l)
		default:
			p.other = append(p.other, l)
		}
	}
	runKey := func(l string) string {
		if f := strings.Fields(l); len(f) > 2 {
			return f[1] + " " + f[2]
		}
		return l
	}
	for i := 0; i < len(p.exec); {
		j := i + 1
		for j < len(p.exec) && runKey(p.exec[j]) == runKey(p.exec[i]) {
			j++
		}
		sort.Strings(p.exec[i:j])
		i = j
	}
	return p
}

// checkGoldenProjected compares got with testdata/transcripts/<name>.golden
// under the projection.
func checkGoldenProjected(t *testing.T, name, got string) {
	t.Helper()
	want, err := os.ReadFile(filepath.Join("testdata", "transcripts", name+".golden"))
	if err != nil {
		t.Fatalf("%v\n%s", err, got)
	}
	w, g := project(string(want)), project(got)
	diff := func(what string, w, g []string) {
		t.Helper()
		for i := range max(len(w), len(g)) {
			var wl, gl string
			if i < len(w) {
				wl = w[i]
			}
			if i < len(g) {
				gl = g[i]
			}
			if wl != gl {
				t.Fatalf("transcript %s, %s differ at line %d:\n  want %q\n  got  %q\n\nwant:\n%s\ngot:\n%s",
					name, what, i+1, wl, gl, want, got)
			}
		}
	}
	diff("the event lines", w.other, g.other)
	diff("the exec lines", w.exec, g.exec)
	diff("the snapshot lines", w.snap, g.snap)
	cats := map[string]bool{}
	for c := range w.logs {
		cats[c] = true
	}
	for c := range g.logs {
		cats[c] = true
	}
	for c := range cats {
		diff("the "+c+" log lines", w.logs[c], g.logs[c])
	}
}
