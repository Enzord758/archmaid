package helper

import (
	"slices"
	"strings"
	"testing"
)

func TestFullCleanupArgs(t *testing.T) {
	h := PlanHash(nil, nil)
	cases := []struct {
		name string
		args string
		ok   bool
	}{
		{"minimal", "--keep 3 --journal 2weeks", true},
		{"all", "--keep 1 --wipe --uninstalled --orphans " + h + " --journal 1month", true},
		{"uninstalled", "--uninstalled --keep 3 --journal 2weeks", true},
		{"duplicate uninstalled", "--keep 3 --uninstalled --uninstalled --journal 2weeks", false},
		{"uninstalled with a value", "--keep 3 --uninstalled=yes --journal 2weeks", false},
		{"any order", "--journal 1week --orphans " + h + " --keep 9", true},
		{"missing keep", "--journal 2weeks", false},
		{"missing journal", "--keep 3", false},
		{"keep 0", "--keep 0 --journal 2weeks", false},
		{"keep missing value", "--journal 2weeks --keep", false},
		{"bad journal", "--keep 3 --journal 1day", false},
		{"bad hash", "--keep 3 --journal 2weeks --orphans sha256:00", false},
		{"duplicate", "--keep 3 --keep 4 --journal 2weeks", false},
		{"duplicate wipe", "--keep 3 --wipe --wipe --journal 2weeks", false},
		{"equals form", "--keep=3 --journal 2weeks", false},
		{"positional", "--keep 3 --journal 2weeks extra", false},
		{"unknown flag", "--keep 3 --journal 2weeks --upgrade", false},
		{"flag as value", "--keep --journal 2weeks", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := orphanFake("", "")
			f.on([]string{Pacman, "-Qdtq"}, reply{exit: 1})
			o, out := rootOpts(t, f)
			code := Run(append([]string{"full-cleanup"}, strings.Fields(c.args)...), o)
			if c.ok && code != ExitOK || !c.ok && code != ExitUsage {
				t.Fatalf("exit %d (ok=%v):\n%s", code, c.ok, out)
			}
			if !c.ok && len(f.queries)+len(f.streams) != 0 {
				t.Error("ran commands for invalid input")
			}
		})
	}
}

func TestFullCleanupSequence(t *testing.T) {
	f := orphanFake("a\n", "a 1-1\ndep 1-1\n")
	o, out := rootOpts(t, f)
	h := PlanHash([]string{"a"}, []string{"a 1-1", "dep 1-1"})
	if code := Run([]string{"full-cleanup", "--keep", "2", "--wipe", "--uninstalled", "--orphans", h, "--journal", "1month"}, o); code != ExitOK {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	want := []string{
		"/usr/bin/pacman -Rns --noconfirm --color always -- a",
		"/usr/bin/paccache -r -k 2",
		"/usr/bin/paccache -r -k 0",
		"/usr/bin/pacman -Sc --noconfirm --color always",
		"/usr/bin/systemd-tmpfiles --clean",
		"/usr/bin/journalctl --vacuum-time=1month",
		"/usr/bin/pacman -Qk --color always",
	}
	if got := join(f.streams); !slices.Equal(got, want) {
		t.Errorf("sequence\n got %q\nwant %q", got, want)
	}
	parts := []string{PartRemoveOrphans, PartPaccache, PartPaccacheWipe, PartCleanUninstalled, PartCleanTmp, PartVacuumJournal, PartCheckFiles}
	evs := events(t, out)
	prog, done := ofType(evs, EventProgress), ofType(evs, EventDone)
	if len(prog) != len(want) || len(done) != len(want) {
		t.Fatalf("progress events = %d, done events = %d, want %d each", len(prog), len(done), len(want))
	}
	for i, p := range prog {
		if p["step"] != float64(i+1) || p["of"] != float64(len(want)) || p["label"] == "" || p["part"] != parts[i] {
			t.Errorf("progress %d = %v", i, p)
		}
	}
	for i, d := range done {
		if d["step"] != float64(i+1) || d["of"] != float64(len(want)) || d["part"] != parts[i] ||
			d["ok"] != true || d["code"] != float64(0) {
			t.Errorf("done %d = %v", i, d)
		}
	}
	// Each part's done comes before the next part's progress.
	var order []string
	for _, e := range evs {
		if e["t"] == EventProgress || e["t"] == EventDone {
			order = append(order, e["t"].(string))
		}
	}
	for i := range order {
		if wantT := []string{EventProgress, EventDone}[i%2]; order[i] != wantT {
			t.Fatalf("event order %q: parts overlap", order)
		}
	}
}

// TestPlainVerbsHaveNoParts: only the combined verb tags its progress with
// a part and sends done events; a plain verb's stream is unchanged.
func TestPlainVerbsHaveNoParts(t *testing.T) {
	f := newFake()
	o, out := rootOpts(t, f)
	if code := Run([]string{"paccache", "3"}, o); code != ExitOK {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	evs := events(t, out)
	if len(ofType(evs, EventDone)) != 0 {
		t.Error("a plain verb sent a done event")
	}
	for _, p := range ofType(evs, EventProgress) {
		if _, ok := p["part"]; ok {
			t.Errorf("a plain verb's progress names a part: %v", p)
		}
	}
}

func TestFullCleanupWithoutOptionalSteps(t *testing.T) {
	f := newFake()
	o, out := rootOpts(t, f)
	if code := Run([]string{"full-cleanup", "--keep", "3", "--journal", "2weeks"}, o); code != ExitOK {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	want := []string{
		"/usr/bin/paccache -r -k 3",
		"/usr/bin/systemd-tmpfiles --clean",
		"/usr/bin/journalctl --vacuum-time=2weeks",
		"/usr/bin/pacman -Qk --color always",
	}
	if got := join(f.streams); !slices.Equal(got, want) {
		t.Errorf("got %q\nwant %q", got, want)
	}
	if len(f.queries) != 0 {
		t.Errorf("planned orphans without --orphans: %q", f.queries)
	}
}

func TestFullCleanupStalePlanSkipsOnlyOrphans(t *testing.T) {
	f := orphanFake("a\nnew\n", "a 1-1\nnew 1-1\n")
	o, out := rootOpts(t, f)
	stale := PlanHash([]string{"a"}, []string{"a 1-1"})
	if code := Run([]string{"full-cleanup", "--keep", "3", "--orphans", stale, "--journal", "2weeks"}, o); code != ExitStale {
		t.Fatalf("exit %d, want 3:\n%s", code, out)
	}
	for _, s := range join(f.streams) {
		if strings.Contains(s, "-Rns") {
			t.Fatalf("removed orphans on a stale plan: %q", f.streams)
		}
	}
	if len(f.streams) != 4 {
		t.Errorf("the other steps must still run, ran %q", f.streams)
	}
	evs := events(t, out)
	if len(ofType(evs, EventPlan)) != 1 {
		t.Error("the new plan is not reported")
	}
	// Only the orphan part is stale; the others are done and ok.
	for _, d := range ofType(evs, EventDone) {
		stale := d["part"] == PartRemoveOrphans
		if stale && (d["ok"] != false || d["code"] != float64(ExitStale)) || !stale && d["ok"] != true {
			t.Errorf("done %v", d)
		}
	}
}

func TestFullCleanupFailureContinuesAndIsExit1(t *testing.T) {
	f := newFake().on(journalArgv("2weeks"), reply{exit: 1, err: "journal broke"})
	f.on(checkFilesArgv(), reply{exit: 1}) // missing files: not a failure
	o, out := rootOpts(t, f)
	if code := Run([]string{"full-cleanup", "--keep", "3", "--journal", "2weeks"}, o); code != ExitFailed {
		t.Fatalf("exit %d, want 1:\n%s", code, out)
	}
	if len(f.streams) != 4 {
		t.Errorf("a failing step stopped the rest: %q", f.streams)
	}
	if res := result(t, out); !strings.Contains(res["error"].(string), "journal") {
		t.Errorf("error does not name the failed step: %v", res["error"])
	}
	// The failure is the journal part's alone.
	for _, d := range ofType(events(t, out), EventDone) {
		failed := d["part"] == PartVacuumJournal
		if failed && (d["ok"] != false || d["code"] != float64(ExitFailed) || !strings.Contains(d["error"].(string), "journalctl")) ||
			!failed && d["ok"] != true {
			t.Errorf("done %v", d)
		}
	}
}
