package helperclient

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/steps"
)

// fullCleanupCall is the step the engine builds for Full Cleanup's combined
// call: its parts in the helper's order, the orphan removal, the cache
// wipe and the uninstalled packages' cache only when asked for.
func fullCleanupCall(orphans, wipe, uninstalled bool) steps.Step {
	part := func(id, verb string) steps.Step {
		return steps.Step{ID: id, Verb: verb, Combine: steps.VerbFullCleanup, NeedsPriv: true}
	}
	var parts []steps.Step
	if orphans {
		p := goodPlan()
		o := part(steps.IDOrphansRemove, steps.VerbRemoveOrphans)
		o.Items, o.Cascade, o.Plan = p.Direct, p.Cascade, &p
		parts = append(parts, o)
	}
	parts = append(parts, part(steps.IDPaccache, steps.VerbPaccache))
	if wipe {
		parts = append(parts, part(steps.IDPaccacheWipe, steps.VerbPaccacheWipe))
	}
	if uninstalled {
		parts = append(parts, part(steps.IDPkgCacheUninstalled, steps.VerbFullCleanup))
	}
	parts = append(parts, part(steps.IDTmpfiles, steps.VerbCleanTmp), part(steps.IDJournal, steps.VerbVacuumJournal),
		part(steps.IDIntegrity, steps.VerbCheckFiles))
	return steps.Step{ID: steps.VerbFullCleanup, Verb: steps.VerbFullCleanup, NeedsPriv: true, Parts: parts}
}

func TestArgvFullCleanup(t *testing.T) {
	got, err := Argv(fullCleanupCall(true, true, true))
	want := []string{Pkexec, DisableInternalAgent, HelperPath, "full-cleanup", "--keep", PaccacheKeep, "--journal", JournalSpan,
		"--orphans", goodPlan().Hash, "--wipe", "--uninstalled"}
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("Argv = %q, %v\nwant %q", got, err, want)
	}
	got, err = Argv(fullCleanupCall(false, false, false))
	if want := want[:8]; err != nil || !slices.Equal(got, want) {
		t.Fatalf("Argv = %q, %v\nwant %q", got, err, want)
	}

	bad := map[string]func(*steps.Step){
		"out of order":           func(s *steps.Step) { s.Parts[0], s.Parts[1] = s.Parts[1], s.Parts[0] },
		"missing":                func(s *steps.Step) { s.Parts = slices.Delete(s.Parts, 4, 5) }, // clean-tmp
		"uninstalled with items": func(s *steps.Step) { s.Parts[3].Items = []string{"a"} },
		"another verb-less part": func(s *steps.Step) { s.Parts[3].ID = steps.IDYayCache },
		"twice":                  func(s *steps.Step) { s.Parts = append(s.Parts, s.Parts[len(s.Parts)-1]) },
		"foreign":                func(s *steps.Step) { s.Parts = append(s.Parts, steps.Step{ID: "x", Verb: steps.VerbRepair}) },
		"not combined":           func(s *steps.Step) { s.Parts[1].Combine = "" },
		"nested":                 func(s *steps.Step) { s.Parts[1].Parts = []steps.Step{{}} },
		"names":                  func(s *steps.Step) { s.Parts[len(s.Parts)-1].Items = []string{"a"} },
		"items":                  func(s *steps.Step) { s.Items = []string{"a"} },
		"bad plan":               func(s *steps.Step) { s.Parts[0].Items = []string{"other"} },
	}
	for name, f := range bad {
		t.Run(name, func(t *testing.T) {
			st := fullCleanupCall(true, true, true)
			f(&st)
			if got, err := Argv(st); err == nil {
				t.Fatalf("Argv accepted it: %q", got)
			}
		})
	}
}

// part lines, as the helper writes them.
func progressLine(n, of int, part, label string) string {
	return fmt.Sprintf(`{"v":1,"t":"progress","step":%d,"of":%d,"label":%q,"part":%q}`, n, of, label, part)
}

func doneLine(n, of int, part string, code int, msg string) string {
	return fmt.Sprintf(`{"v":1,"t":"done","step":%d,"of":%d,"part":%q,"ok":%v,"code":%d,"error":%q}`,
		n, of, part, code == 0, code, msg)
}

// fullCleanupStream is a whole full-cleanup run over the parts of
// fullCleanupCall(true, true, true); fail names a part that fails (exit 1), and
// stale makes the orphan part find a changed plan.
func fullCleanupStream(fail string, stale bool) (code string, ls []string) {
	parts := []string{"remove-orphans", "paccache", "paccache-wipe", "clean-uninstalled", "clean-tmp", "vacuum-journal", "check-files"}
	ls = []string{`{"v":1,"t":"start","verb":"full-cleanup","version":"0.1.0"}`}
	res := 0
	for i, p := range parts {
		ls = append(ls, progressLine(i+1, len(parts), p, "doing "+p),
			fmt.Sprintf(`{"v":1,"t":"line","stream":"out","text":"out of %s"}`, p))
		c, msg := 0, ""
		switch {
		case p == fail:
			c, msg, res = 1, p+" broke", 1
		case p == "remove-orphans" && stale:
			c, msg = 3, "orphan plan changed since confirm; nothing removed"
			if res == 0 {
				res = 3
			}
		}
		ls = append(ls, doneLine(i+1, len(parts), p, c, msg))
	}
	ls = append(ls, fmt.Sprintf(`{"v":1,"t":"result","ok":%v,"code":%d,"child_exit":0,"skipped":[{"name":"remove-orphans","reason":"x"}]}`,
		res == 0, res))
	return fmt.Sprint(res), ls
}

// trace is the inputs of a run as short strings, StepOutput by line.
func trace(r recorded) []string {
	var out []string
	for _, in := range r.inputs {
		switch in := in.(type) {
		case engine.PrivilegeGranted:
			out = append(out, "granted")
		case engine.StepOutput:
			for _, l := range in.Lines {
				out = append(out, "| "+l)
			}
		case engine.PartStarted:
			out = append(out, "start "+in.Step)
		case engine.PartDone:
			s := "done " + in.Step
			if in.Err != nil {
				s += ": " + in.Err.Error()
			}
			out = append(out, s)
		case engine.StepExited:
			out = append(out, fmt.Sprintf("exited %v", in.Err))
		}
	}
	return out
}

// TestRunFullCleanup: one pkexec call; each part's lines come between its
// start and its done, in order; the last part ends with StepExited.
func TestRunFullCleanup(t *testing.T) {
	code, ls := fullCleanupStream("", false)
	argvFile := shim(t, lines(code, ls...))
	r := runStep(t, fullCleanupCall(true, true, true))
	if got := readArgv(t, argvFile); got[0] != DisableInternalAgent || got[2] != "full-cleanup" {
		t.Errorf("pkexec argv %q", got)
	}
	want := []string{"granted",
		"| doing remove-orphans", "| out of remove-orphans", "done orphans-remove",
		"start paccache", "| doing paccache", "| out of paccache", "done paccache",
		"start paccache-wipe", "| doing paccache-wipe", "| out of paccache-wipe", "done paccache-wipe",
		"start pkg-cache-uninstalled", "| doing clean-uninstalled", "| out of clean-uninstalled", "done pkg-cache-uninstalled",
		"start tmpfiles", "| doing clean-tmp", "| out of clean-tmp", "done tmpfiles",
		"start journal", "| doing vacuum-journal", "| out of vacuum-journal", "done journal",
		"start integrity", "| doing check-files", "| out of check-files",
		"exited <nil>"}
	if got := trace(r); !slices.Equal(got, want) {
		t.Errorf("inputs\n got %q\nwant %q", got, want)
	}
}

// TestRunFullCleanupPartOutcomes: a failed part and a stale orphan plan are
// those parts' own errors, the rest are fine, and the call's StepExited
// carries only the last part's.
func TestRunFullCleanupPartOutcomes(t *testing.T) {
	for _, c := range []struct {
		fail  string
		stale bool
		step  string
		want  string
	}{
		{"paccache", true, "paccache", "root helper (exit 1): paccache broke"},
		{"", true, "orphans-remove", ErrOrphansChanged.Error()},
	} {
		code, ls := fullCleanupStream(c.fail, c.stale)
		shim(t, lines(code, ls...))
		r := runStep(t, fullCleanupCall(true, true, true))
		if e := r.exited(t); e.Err != nil {
			t.Errorf("StepExited.Err = %v, want nil (the last part was fine)", e.Err)
		}
		for _, in := range r.inputs {
			d, ok := in.(engine.PartDone)
			if !ok {
				continue
			}
			got := ""
			if d.Err != nil {
				got = d.Err.Error()
			}
			switch {
			case d.Step == c.step && got != c.want:
				t.Errorf("%s: %q, want %q", d.Step, got, c.want)
			case d.Step == "orphans-remove" && c.stale && !errors.Is(d.Err, ErrOrphansChanged):
				t.Errorf("stale orphans: %v", d.Err)
			case d.Step != c.step && d.Step != "orphans-remove" && d.Err != nil:
				t.Errorf("%s failed: %v", d.Step, d.Err)
			}
		}
	}
	// The last part failing is the call's error.
	code, ls := fullCleanupStream("check-files", false)
	shim(t, lines(code, ls...))
	if e := runStep(t, fullCleanupCall(true, true, true)).exited(t); e.Err == nil || !strings.Contains(e.Err.Error(), "check-files broke") {
		t.Errorf("StepExited.Err = %v", e.Err)
	}
}

// TestRunFullCleanupProtocol: parts out of order, a result that disagrees
// with the parts, a result before the parts are done, and a done for a
// part that isn't running all fail the call closed.
func TestRunFullCleanupProtocol(t *testing.T) {
	_, ls := fullCleanupStream("", false)
	start, parts, result := ls[0], ls[1:len(ls)-1], ls[len(ls)-1]
	cases := map[string][]string{
		"skipped part":    append([]string{start}, append(slices.Clone(parts[3:]), result)...),
		"result disagree": append([]string{start}, append(slices.Clone(parts), strings.Replace(strings.Replace(result, `"ok":true`, `"ok":false`, 1), `"code":0`, `"code":1`, 1))...),
		"early result":    {start, parts[0], parts[1], parts[2], result},
		"done twice":      append([]string{start}, append(append(slices.Clone(parts[:3]), parts[2]), result)...),
		"wrong part":      {start, progressLine(1, 7, "paccache", "x"), result},
		"wrong of":        {start, progressLine(1, 6, "remove-orphans", "x"), result},
	}
	for name, ls := range cases {
		t.Run(name, func(t *testing.T) {
			code := "0"
			if name == "result disagree" {
				code = "1"
			}
			shim(t, lines(code, ls...))
			if e := runStep(t, fullCleanupCall(true, true, true)).exited(t); e.Err == nil || e.NotGranted {
				t.Fatalf("StepExited = %+v, want a failure", e)
			}
		})
	}
}

// TestRunFullCleanupRefused: a refused dialog is NotGranted, with no part
// event at all; a refusal at the helper's lock is the call's error.
func TestRunFullCleanupRefused(t *testing.T) {
	shim(t, "exit 127")
	r := runStep(t, fullCleanupCall(false, false, false))
	if e := r.exited(t); !e.NotGranted || len(r.inputs) != 1 {
		t.Fatalf("inputs %#v", r.inputs)
	}
	shim(t, lines("5", `{"v":1,"t":"start","verb":"full-cleanup","version":"0.1.0"}`,
		`{"v":1,"t":"result","ok":false,"code":5,"child_exit":null,"skipped":[],"error":"pacman is running"}`))
	if e := runStep(t, fullCleanupCall(false, false, false)).exited(t); e.Err == nil || !strings.Contains(e.Err.Error(), "pacman is running") {
		t.Fatalf("StepExited.Err = %v", e.Err)
	}
}

// TestRunFullCleanupNoAgent: the combined call carries the no-agent reason
// too (the engine settles every part it covered with it).
func TestRunFullCleanupNoAgent(t *testing.T) {
	shim(t, "echo 'Error executing command as another user: No authentication agent found.' >&2\nexit 127")
	ex := runStep(t, fullCleanupCall(true, true, true)).exited(t)
	if !ex.NotGranted || !errors.Is(ex.Err, ErrNotGranted) || !errors.Is(ex.Err, engine.ErrNoAuthAgent) {
		t.Fatalf("StepExited %+v, want NotGranted with ErrNoAuthAgent", ex)
	}
}
