package realdriver

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/logstore"
)

// shimTopgrade puts a topgrade shim first on PATH (testenv traps topgrade,
// so without the shim a run would reach the trap and fail the package)
// and returns the file it appends a line to per run.
func shimTopgrade(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "topgrade.log")
	body := "#!/bin/sh\necho ran >> '" + log + "'\necho 'topgrade shim: updating'\n"
	if err := os.WriteFile(filepath.Join(dir, "topgrade"), []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

// minutes is a clock that steps a minute per read, from a fixed start, so
// every Driver's run directory gets its own stamp.
func minutes(start time.Time) func() time.Time {
	var n atomic.Int64
	return func() time.Time { return start.Add(time.Duration(n.Add(1)) * time.Minute) }
}

// runAction starts action on d and plays it to Finished: every confirm is
// answered with yes unless confirm says otherwise. It returns the outcome.
func runAction(t *testing.T, d engine.Driver, action string, confirm func(engine.NeedsConfirm) bool) engine.Outcome {
	t.Helper()
	d.Send(engine.Start{Action: action})
	timeout := time.After(20 * time.Second)
	for {
		select {
		case ev, ok := <-d.Events():
			if !ok {
				t.Fatal("Events closed early")
			}
			switch ev := ev.(type) {
			case engine.NeedsConfirm:
				yes := true
				if confirm != nil {
					yes = confirm(ev)
				}
				d.Send(engine.Confirm{Prompt: ev.ID, Yes: yes})
			case engine.Finished:
				return ev.Outcome
			}
		case <-timeout:
			t.Fatalf("%s: no Finished", action)
		}
	}
}

// runOn runs action on a fresh Driver for o, as the window does per
// Start, and closes it whatever happens (a failure included), so nothing
// it started outlives the test.
func runOn(t *testing.T, w *Wiring, o engine.Options, action string) engine.Outcome {
	t.Helper()
	d := w.New(o)
	defer d.Close()
	return runAction(t, d, action, nil)
}

// runDirs lists the state dir's run directories (stamped ones and the
// dryrun slot), sorted.
func runDirs(t *testing.T, root string) []string {
	t.Helper()
	ents, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	var dirs []string
	for _, e := range ents {
		if e.IsDir() {
			dirs = append(dirs, e.Name())
		}
	}
	return dirs
}

// TestRunDirs: a long GUI session (one Wiring, a fresh Driver per Start,
// as the window makes them) puts each real run in its own run directory,
// stamped when the run's Driver was made; KeepRuns+1 real runs leave the
// KeepRuns newest; last-run.json stays in the state dir's root; a dry run
// writes to the dryrun slot and leaves the real history as it was.
func TestRunDirs(t *testing.T) {
	ran := shimTopgrade(t)
	start := time.Date(2026, 10, 4, 9, 0, 0, 0, time.Local)
	w := Start(fakes(t, Config{NoAskpass: true, Now: minutes(start)}))
	defer w.Close()
	root := w.cfg.StateDir
	base := w.Options() // reads the clock once: start+1m names nothing on disk

	var stamps []string
	for i := range logstore.KeepRuns + 1 {
		if out := runOn(t, w, base, actions.IDTopgrade); out != engine.OutcomeCompleted {
			t.Fatalf("run %d: outcome %v", i, out)
		}
		stamps = append(stamps, start.Add(time.Duration(i+2)*time.Minute).Format("20060102-1504"))
		if got := runDirs(t, root); i < logstore.KeepRuns && !slices.Equal(got, stamps) {
			t.Fatalf("after run %d: run dirs %q, want %q", i, got, stamps)
		}
		for _, f := range []string{"audit.log", "topgrade.log"} {
			if _, err := os.Stat(filepath.Join(root, stamps[i], f)); err != nil {
				t.Errorf("run %d: %v", i, err)
			}
		}
	}
	want := stamps[1:]
	if got := runDirs(t, root); !slices.Equal(got, want) {
		t.Errorf("after %d runs: run dirs %q, want the %d newest %q", len(stamps), got, logstore.KeepRuns, want)
	}
	if b, err := os.ReadFile(ran); err != nil || strings.Count(string(b), "ran\n") != len(stamps) {
		t.Errorf("the topgrade shim ran %q (%v), want %d times", b, err, len(stamps))
	}
	if r, err := logstore.New(logstore.Options{Dir: root}).ReadLastRun(); err != nil || r.Action != actions.IDTopgrade || r.DryRun {
		t.Errorf("last-run.json in the root: %+v, %v", r, err)
	}
	for _, s := range want {
		if _, err := os.Stat(filepath.Join(root, s, logstore.LastRunFile)); err == nil {
			t.Errorf("a last-run.json in run dir %s", s)
		}
	}

	dry := base
	dry.DryRun = true
	if out := runOn(t, w, dry, actions.IDTopgrade); out != engine.OutcomeCompleted {
		t.Fatalf("dry run: outcome %v", out)
	}
	if got := runDirs(t, root); !slices.Equal(got, append(slices.Clone(want), "dryrun")) {
		t.Errorf("after a dry run: run dirs %q, want %q and dryrun", got, want)
	}
	if _, err := os.Stat(filepath.Join(root, "dryrun", "audit.log")); err != nil {
		t.Errorf("the dry run's audit log: %v", err)
	}
	if b, _ := os.ReadFile(ran); strings.Count(string(b), "ran\n") != len(stamps) {
		t.Errorf("a dry run ran the topgrade shim")
	}
}

// blockingForeign is a Config.Foreign that records each call and blocks
// until its ctx ends (the audit cancelled) or release closes; then it
// reports no foreign packages.
type blockingForeign struct {
	calls     atomic.Int32
	entered   chan struct{} // gets one value per call
	release   chan struct{}
	cancelled atomic.Int32
	returned  atomic.Int32
}

func newBlockingForeign() *blockingForeign {
	return &blockingForeign{entered: make(chan struct{}, 8), release: make(chan struct{})}
}

func (f *blockingForeign) list(ctx context.Context) ([]string, error) {
	defer f.returned.Add(1)
	f.calls.Add(1)
	f.entered <- struct{}{}
	select {
	case <-ctx.Done():
		f.cancelled.Add(1)
		return nil, ctx.Err()
	case <-f.release:
		return nil, nil
	}
}

func (f *blockingForeign) waitEntered(t *testing.T) {
	t.Helper()
	select {
	case <-f.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the audit never asked for the foreign packages")
	}
}

// gateUp starts Full Cleanup on d and returns its gate's prompt once it
// is up and the prefetched audit is under way behind it.
func gateUp(t *testing.T, d engine.Driver, f *blockingForeign) engine.PromptID {
	t.Helper()
	d.Send(engine.Start{Action: actions.IDFullCleanup})
	timeout := time.After(10 * time.Second)
	for {
		select {
		case ev, ok := <-d.Events():
			if !ok {
				t.Fatal("Events closed early")
			}
			if c, ok := ev.(engine.NeedsConfirm); ok {
				if c.Kind != engine.ConfirmGate {
					t.Fatalf("first confirm %+v, want the gate", c)
				}
				f.waitEntered(t) // the lookup runs while the gate is up
				return c.ID
			}
		case <-timeout:
			t.Fatal("no gate")
		}
	}
}

// dryOptions are the Wiring's base Options as a dry run: Full Cleanup's
// helper steps then start nothing (no pkexec), whatever the gate's answer.
func dryOptions(w *Wiring) engine.Options {
	o := w.Options()
	o.DryRun = true
	return o
}

// TestAuditPrefetch: the AUR audit's lookup starts at the gate (the
// queue's StartPrefetch), and the audit step consumes it: one lookup per
// run, its result the step's. The AUR is never reached (no foreign
// packages); Foreign stands in for pacman.
func TestAuditPrefetch(t *testing.T) {
	f := newBlockingForeign()
	w := Start(fakes(t, Config{NoAskpass: true}))
	w.cfg.Foreign = f.list
	defer w.Close()
	d := w.New(dryOptions(w))
	defer d.Close()

	gate := gateUp(t, d, f)
	close(f.release)
	d.Send(engine.Confirm{Prompt: gate, Yes: true})
	var auditOut []string
	timeout := time.After(20 * time.Second)
	for done := false; !done; {
		select {
		case ev := <-d.Events():
			switch ev := ev.(type) {
			case engine.OutputChunk:
				auditOut = append(auditOut, ev.Lines...)
			case engine.NeedsConfirm: // the dry run's later confirms
				d.Send(engine.Confirm{Prompt: ev.ID, Yes: true})
			case engine.Finished:
				if ev.Outcome != engine.OutcomeCompleted {
					t.Errorf("outcome %v", ev.Outcome)
				}
				done = true
			}
		case <-timeout:
			t.Fatal("no Finished")
		}
	}
	if n := f.calls.Load(); n != 1 {
		t.Errorf("the audit looked up %d times, want once (the prefetch, consumed by the step)", n)
	}
	if !slices.ContainsFunc(auditOut, func(l string) bool { return strings.Contains(l, "nothing to audit") }) {
		t.Errorf("output %q: want the prefetched audit's result", auditOut)
	}
}

// TestAuditPrefetchCancelled: a declined gate cancels the prefetched
// lookup and the Driver reaps it; so does Close with the gate still up.
// Either way nothing of it outlives the Driver.
func TestAuditPrefetchCancelled(t *testing.T) {
	for _, how := range []string{"decline", "close"} {
		t.Run(how, func(t *testing.T) {
			f := newBlockingForeign()
			w := Start(fakes(t, Config{NoAskpass: true}))
			w.cfg.Foreign = f.list
			defer w.Close()
			d := w.New(dryOptions(w))
			defer d.Close() // also on a failure: nothing the Driver started outlives the test
			gate := gateUp(t, d, f)
			if how == "decline" {
				d.Send(engine.Confirm{Prompt: gate, Yes: false})
				timeout := time.After(10 * time.Second)
				for fin := false; !fin; {
					select {
					case ev := <-d.Events():
						if e, ok := ev.(engine.Finished); ok {
							fin = true
							if e.Outcome != engine.OutcomeCancelled {
								t.Errorf("outcome %v, want cancelled", e.Outcome)
							}
						}
					case <-timeout:
						t.Fatal("no Finished after the declined gate")
					}
				}
				deadline := time.Now().Add(10 * time.Second)
				for f.returned.Load() == 0 && time.Now().Before(deadline) {
					time.Sleep(5 * time.Millisecond)
				}
			}
			d.Close() // reaps the prefetch: it has returned by now
			if f.cancelled.Load() != 1 || f.returned.Load() != 1 || f.calls.Load() != 1 {
				t.Errorf("lookups %d, cancelled %d, returned %d; want the one prefetch cancelled and reaped",
					f.calls.Load(), f.cancelled.Load(), f.returned.Load())
			}
		})
	}
}
