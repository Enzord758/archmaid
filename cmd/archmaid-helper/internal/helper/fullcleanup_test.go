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
		{"all", "--keep 1 --wipe --orphans " + h + " --journal 1month", true},
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
	if code := Run([]string{"full-cleanup", "--keep", "2", "--wipe", "--orphans", h, "--journal", "1month"}, o); code != ExitOK {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	want := []string{
		"/usr/bin/paccache -r -k 2",
		"/usr/bin/paccache -r -k 0",
		"/usr/bin/pacman -Rns --noconfirm -- a",
		"/usr/bin/journalctl --vacuum-time=1month",
		"/usr/bin/systemd-tmpfiles --clean",
		"/usr/bin/pacman -Qk",
	}
	if got := join(f.streams); !slices.Equal(got, want) {
		t.Errorf("sequence\n got %q\nwant %q", got, want)
	}
	prog := ofType(events(t, out), EventProgress)
	if len(prog) != len(want) {
		t.Fatalf("progress events = %d, want %d", len(prog), len(want))
	}
	for i, p := range prog {
		if p["step"] != float64(i+1) || p["of"] != float64(len(want)) || p["label"] == "" {
			t.Errorf("progress %d = %v", i, p)
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
		"/usr/bin/journalctl --vacuum-time=2weeks",
		"/usr/bin/systemd-tmpfiles --clean",
		"/usr/bin/pacman -Qk",
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
	if len(ofType(events(t, out), EventPlan)) != 1 {
		t.Error("the new plan is not reported")
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
}
