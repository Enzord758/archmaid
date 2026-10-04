package helper

import (
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strings"
	"testing"
)

var hash0 = "sha256:" + strings.Repeat("0", 64)

// TestVerbs pins each simple verb's validation and the exact argv it runs.
func TestVerbs(t *testing.T) {
	cases := []struct {
		name string
		args []string
		code int
		argv string // the one streamed command; "" = nothing may run
	}{
		{"check-files", []string{"check-files"}, ExitOK, "/usr/bin/pacman -Qk"},
		{"check-files name not installed", []string{"check-files", "x"}, ExitUsage, ""},
		{"check-files flag", []string{"check-files", "--root=/"}, ExitUsage, ""},
		{"paccache 3", []string{"paccache", "3"}, ExitOK, "/usr/bin/paccache -r -k 3"},
		{"paccache 1", []string{"paccache", "1"}, ExitOK, "/usr/bin/paccache -r -k 1"},
		{"paccache 9", []string{"paccache", "9"}, ExitOK, "/usr/bin/paccache -r -k 9"},
		{"paccache 0 refused", []string{"paccache", "0"}, ExitUsage, ""},
		{"paccache 10 refused", []string{"paccache", "10"}, ExitUsage, ""},
		{"paccache flag injection", []string{"paccache", "-k0"}, ExitUsage, ""},
		{"paccache missing N", []string{"paccache"}, ExitUsage, ""},
		{"paccache two args", []string{"paccache", "3", "4"}, ExitUsage, ""},
		{"paccache-wipe", []string{"paccache-wipe"}, ExitOK, "/usr/bin/paccache -r -k 0"},
		{"paccache-wipe extra", []string{"paccache-wipe", "1"}, ExitUsage, ""},
		{"vacuum 1week", []string{"vacuum-journal", "1week"}, ExitOK, "/usr/bin/journalctl --vacuum-time=1week"},
		{"vacuum 2weeks", []string{"vacuum-journal", "2weeks"}, ExitOK, "/usr/bin/journalctl --vacuum-time=2weeks"},
		{"vacuum 1month", []string{"vacuum-journal", "1month"}, ExitOK, "/usr/bin/journalctl --vacuum-time=1month"},
		{"vacuum 1s refused", []string{"vacuum-journal", "1s"}, ExitUsage, ""},
		{"vacuum injection", []string{"vacuum-journal", "1week --rotate"}, ExitUsage, ""},
		{"vacuum missing", []string{"vacuum-journal"}, ExitUsage, ""},
		{"clean-tmp", []string{"clean-tmp"}, ExitOK, "/usr/bin/systemd-tmpfiles --clean"},
		{"clean-tmp extra", []string{"clean-tmp", "--remove"}, ExitUsage, ""},
		{"upgrade-repos is gone (topgrade is the only update path)", []string{"upgrade-repos"}, ExitUsage, ""},
		{"no verb", nil, ExitUsage, ""},
		{"unknown verb", []string{"rm-rf"}, ExitUsage, ""},
		{"verb case matters", []string{"Check-Files"}, ExitUsage, ""},
		{"remove-orphans bad hash", []string{"remove-orphans", "sha256:xyz"}, ExitUsage, ""},
		{"remove-orphans upper hex", []string{"remove-orphans", "sha256:" + strings.Repeat("A", 64)}, ExitUsage, ""},
		{"remove-orphans no hash", []string{"remove-orphans"}, ExitUsage, ""},
		{"plan-orphans extra", []string{"plan-orphans", "x"}, ExitUsage, ""},
		// A unit name never starts with '-' (nor '.'), although every
		// systemctl argv puts "--" before the names anyway.
		{"disable-unit dash", []string{"disable-unit", "-x.service"}, ExitUsage, ""},
		{"disable-unit flag", []string{"disable-unit", "--now.service"}, ExitUsage, ""},
		{"disable-unit flag after a good one", []string{"disable-unit", "a.service", "--now.service"}, ExitUsage, ""},
		{"disable-unit dot", []string{"disable-unit", ".x.service"}, ExitUsage, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFake()
			o, out := rootOpts(t, f)
			if code := Run(c.args, o); code != c.code {
				t.Fatalf("exit %d, want %d; output:\n%s", code, c.code, out)
			}
			res := result(t, out)
			if res["code"] != float64(c.code) || res["ok"] != (c.code == ExitOK) {
				t.Errorf("result %v does not match exit %d", res, c.code)
			}
			got := join(f.streams)
			if c.argv == "" {
				if len(got) != 0 {
					t.Errorf("ran %q, want nothing", got)
				}
				return
			}
			if !slices.Equal(got, []string{c.argv}) {
				t.Errorf("ran %q, want %q", got, c.argv)
			}
		})
	}
}

func TestCheckFilesMissingFilesIsAResult(t *testing.T) {
	f := newFake().on(checkFilesArgv(), reply{out: "a: 3 total files, 1 missing file\n", exit: 1})
	o, out := rootOpts(t, f)
	if code := Run([]string{"check-files"}, o); code != ExitOK {
		t.Fatalf("exit %d, want 0 (pacman -Qk exit 1 = found missing files)", code)
	}
	if res := result(t, out); res["child_exit"] != float64(1) {
		t.Errorf("child_exit = %v, want 1", res["child_exit"])
	}
}

func TestRootCommandFailureIsExit1(t *testing.T) {
	f := newFake().on(paccacheArgv("3"), reply{err: "boom", exit: 2})
	o, out := rootOpts(t, f)
	if code := Run([]string{"paccache", "3"}, o); code != ExitFailed {
		t.Fatalf("exit %d, want 1", code)
	}
	res := result(t, out)
	if res["child_exit"] != float64(2) || res["ok"] != false {
		t.Errorf("result = %v", res)
	}
	if lines := ofType(events(t, out), EventLine); len(lines) != 1 || lines[0]["stream"] != "err" || lines[0]["text"] != "boom" {
		t.Errorf("stderr not relayed as a line event: %v", lines)
	}
}

// ---- orphans ---------------------------------------------------------------

func orphanFake(targets, printed string) *fakeRunner {
	f := newFake().on([]string{Pacman, "-Qdtq"}, reply{out: targets})
	ts := nonEmptyLines(targets)
	slices.Sort(ts)
	argv := append([]string{Pacman, "-Rs", "--print", "--print-format", "%n %v", "--"}, ts...)
	return f.on(argv, reply{out: printed})
}

func TestPlanOrphans(t *testing.T) {
	f := orphanFake("b\na\n", "a 1.0-1\nb 2:3.4-2\nlibdep 0.1-1\n")
	o, out := rootOpts(t, f)
	o.Euid, o.CallerUID, o.Exe, o.Stat = 1000, "", nil, nil // no root, no install check
	if code := Run([]string{"plan-orphans"}, o); code != ExitOK {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	plans := ofType(events(t, out), EventPlan)
	if len(plans) != 1 {
		t.Fatalf("want one plan event, got %v", plans)
	}
	p := plans[0]
	if p["total"] != float64(3) || len(p["items"].([]any)) != 3 || len(p["targets"].([]any)) != 2 {
		t.Errorf("plan = %v", p)
	}
	want := PlanHash([]string{"a", "b"}, []string{"a 1.0-1", "b 2:3.4-2", "libdep 0.1-1"})
	if p["hash"] != want {
		t.Errorf("hash = %v, want %v", p["hash"], want)
	}
	if len(f.streams) != 0 {
		t.Errorf("plan-orphans must not run anything: %q", f.streams)
	}
	for _, q := range f.queries {
		if slices.Contains(q, "-Rns") || !slices.Contains(q, "-Qdtq") && !slices.Contains(q, "--print") {
			t.Errorf("non-read-only query %q", q)
		}
	}
}

func TestPlanOrphansAllowsUppercaseLocalPackages(t *testing.T) {
	f := orphanFake("MyTool-git\n", "MyTool-git r12.abc-1\nlibFoo 1.0-1\n")
	p, err := ComputePlan(f)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(p.Targets, []string{"MyTool-git"}) || len(p.Items) != 2 {
		t.Errorf("plan = %+v", p)
	}
}

// TestPlanOrphansAsRoot: plan-orphans needs no root, but run as root it must
// be the installed binary like every other root run.
func TestPlanOrphansAsRoot(t *testing.T) {
	t.Run("installed", func(t *testing.T) {
		o, out := rootOpts(t, orphanFake("", ""))
		if code := Run([]string{"plan-orphans"}, o); code != ExitOK {
			t.Fatalf("exit %d:\n%s", code, out)
		}
	})
	for name, mutate := range map[string]func(o *Options){
		"wrong path":      func(o *Options) { o.Exe = func() (string, error) { return "/tmp/archmaid-helper", nil } },
		"no install seam": func(o *Options) { o.Exe = nil },
		"user-owned": func(o *Options) {
			o.Stat = func(p string) (FileStat, error) {
				if p == InstallPath {
					return FileStat{Uid: 1000, Mode: 0o755}, nil
				}
				return rootStat(p)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := orphanFake("a\n", "a 1-1\n")
			o, out := rootOpts(t, f)
			mutate(&o)
			if code := Run([]string{"plan-orphans"}, o); code != ExitRefused {
				t.Fatalf("exit %d, want 4:\n%s", code, out)
			}
			if len(f.queries) != 0 {
				t.Errorf("ran %q", f.queries)
			}
		})
	}
}

func TestPlanHashIsOrderIndependentAndCommitsToCascade(t *testing.T) {
	a := PlanHash([]string{"x", "y"}, []string{"x 1-1", "y 1-1"})
	if b := PlanHash([]string{"y", "x"}, []string{"y 1-1", "x 1-1"}); a != b {
		t.Error("hash depends on order")
	}
	if b := PlanHash([]string{"x", "y"}, []string{"x 1-1", "y 1-1", "z 1-1"}); a == b {
		t.Error("hash ignores the cascade")
	}
	if b := PlanHash([]string{"x", "y"}, []string{"x 1-2", "y 1-1"}); a == b {
		t.Error("hash ignores versions")
	}
	if !hashPattern.MatchString(a) {
		t.Errorf("hash %q does not match the input pattern", a)
	}
}

func TestPlanRejectsOddPacmanOutput(t *testing.T) {
	for name, f := range map[string]*fakeRunner{
		"bad target":  orphanFake("ok\n-rf\n", ""),
		"bad item":    orphanFake("a\n", "a 1.0-1 extra\x1b[31m\n"),
		"print fails": orphanFake("a\n", "").on([]string{Pacman, "-Rs", "--print", "--print-format", "%n %v", "--", "a"}, reply{exit: 1, err: "error: x"}),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ComputePlan(f); err == nil {
				t.Error("want an error")
			}
		})
	}
}

func TestRemoveOrphansHashMatch(t *testing.T) {
	f := orphanFake("b\na\n", "a 1-1\nb 1-1\n")
	o, out := rootOpts(t, f)
	h := PlanHash([]string{"a", "b"}, []string{"a 1-1", "b 1-1"})
	if code := Run([]string{"remove-orphans", h}, o); code != ExitOK {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if got := join(f.streams); !slices.Equal(got, []string{"/usr/bin/pacman -Rns --noconfirm -- a b"}) {
		t.Errorf("ran %q", got)
	}
}

func TestRemoveOrphansHashMismatchIsExit3(t *testing.T) {
	// The user confirmed a plan without "c"; a new orphan appeared since.
	confirmed := PlanHash([]string{"a"}, []string{"a 1-1"})
	f := orphanFake("a\nc\n", "a 1-1\nc 1-1\n")
	o, out := rootOpts(t, f)
	if code := Run([]string{"remove-orphans", confirmed}, o); code != ExitStale {
		t.Fatalf("exit %d, want 3:\n%s", code, out)
	}
	if len(f.streams) != 0 {
		t.Fatalf("removed despite a changed plan: %q", f.streams)
	}
	evs := events(t, out)
	if plans := ofType(evs, EventPlan); len(plans) != 1 || plans[0]["hash"] == confirmed {
		t.Errorf("want the new plan reported, got %v", plans)
	}
	if _, ok := skippedNames(t, out)["remove-orphans"]; !ok {
		t.Error("remove-orphans not reported as skipped")
	}
}

// hookRunner is a fakeRunner that calls hook on every command first.
type hookRunner struct {
	*fakeRunner
	hook func(argv []string)
}

func (h hookRunner) Query(argv []string) (string, string, int, error) {
	h.hook(argv)
	return h.fakeRunner.Query(argv)
}

func (h hookRunner) Stream(argv []string, line func(stream, text string)) (int, error) {
	h.hook(argv)
	return h.fakeRunner.Stream(argv, line)
}

// TestRemoveOrphansPacmanStartsDuringRecompute: a pacman that takes its
// lock while the plan is being recomputed (after the verb's own lock
// check) stops the removal: -Rns never runs, exit 5. In full-cleanup, where
// the removal is the first part, the same pacman fails just that part; the
// other parts still run. Fails on a helper that checks db.lck only when the
// verb starts.
func TestRemoveOrphansPacmanStartsDuringRecompute(t *testing.T) {
	h := PlanHash([]string{"a"}, []string{"a 1-1"})
	for name, c := range map[string]struct {
		args    []string
		trigger string // the command during which pacman starts
		code    int
	}{
		"remove-orphans": {[]string{"remove-orphans", h}, "/usr/bin/pacman -Rs --print", ExitBusy},
		"full-cleanup":   {[]string{"full-cleanup", "--keep", "3", "--orphans", h, "--journal", "2weeks"}, "/usr/bin/pacman -Rs --print", ExitFailed},
	} {
		t.Run(name, func(t *testing.T) {
			f := orphanFake("a\n", "a 1-1\n")
			var o Options
			r := hookRunner{f, func(argv []string) {
				if strings.HasPrefix(strings.Join(argv, " "), c.trigger) {
					if err := os.WriteFile(o.PacmanDBLock, nil, 0o600); err != nil {
						t.Error(err)
					}
				}
			}}
			o, out := rootOpts(t, r)
			if code := Run(c.args, o); code != c.code {
				t.Fatalf("exit %d, want %d:\n%s", code, c.code, out)
			}
			for _, s := range join(f.streams) {
				if strings.Contains(s, "-Rns") {
					t.Fatalf("ran %q while pacman was running", s)
				}
			}
			if reason := skippedNames(t, out)["remove-orphans"]; reason != "pacman is running" {
				t.Errorf("remove-orphans skipped with %q", reason)
			}
			if !strings.Contains(result(t, out)["error"].(string), "pacman is running") {
				t.Errorf("result %v", result(t, out))
			}
			if name == "full-cleanup" && len(f.streams) != 4 {
				t.Errorf("ran %q, want the other four steps", join(f.streams))
			}
		})
	}
}

func TestRemoveOrphansNoneLeft(t *testing.T) {
	f := newFake().on([]string{Pacman, "-Qdtq"}, reply{exit: 1})
	o, out := rootOpts(t, f)
	if code := Run([]string{"remove-orphans", PlanHash(nil, nil)}, o); code != ExitOK {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if len(f.streams) != 0 {
		t.Errorf("ran %q", f.streams)
	}
}

// ---- repair ----------------------------------------------------------------

func pq(args ...string) []string { return append([]string{Pacman}, args...) }

// siInfo is `pacman -Si` output for pkg in one repo.
func siInfo(repo, pkg, version string) string {
	return "Repository      : " + repo + "\nName            : " + pkg + "\nVersion         : " + version +
		"\nDescription     : x: y\nArchitecture    : x86_64\n\n"
}

// onPkg fakes pkg as installed at installed, in core at repo (unless repo is
// ""), with missing files missing.
func onPkg(f *fakeRunner, pkg, installed, repo string, missing int) *fakeRunner {
	f.on(pq("-Q", "--", pkg), reply{out: pkg + " " + installed + "\n"})
	if repo != "" {
		f.on(pq("-Si", "--", pkg), reply{out: siInfo("core", pkg, repo)})
	}
	qk := reply{out: fmt.Sprintf("%s: 10 total files, %d missing files\n", pkg, missing)}
	if missing > 0 {
		qk.exit = 1
	}
	return f.on(pq("-Qk", "--", pkg), qk)
}

// onPlan fakes `pacman -Sp` for the sorted broken set pkgs, printing lines
// ("name version").
func onPlan(f *fakeRunner, pkgs []string, lines ...string) *fakeRunner {
	return f.on(repairPlanArgv(pkgs), reply{out: strings.Join(lines, "\n") + "\n"})
}

func TestRepair(t *testing.T) {
	f := newFake()
	// broken: installed, in repo at the same version, missing files
	onPkg(f, "broken", "1.0-1", "1.0-1", 2)
	onPlan(f, []string{"broken"}, "broken 1.0-1")
	// healthy: installed, in repo, nothing missing
	onPkg(f, "healthy", "1.0-1", "1.0-1", 0)
	// aur: installed but not in a repo
	onPkg(f, "aur", "1.0-1", "", 2).on(pq("-Si", "--", "aur"), reply{exit: 1, err: "error: package 'aur' was not found"})
	// gone: not installed
	f.on(pq("-Q", "--", "gone"), reply{exit: 1})
	// odd: -Qk output unparseable
	onPkg(f, "odd", "1.0-1", "1.0-1", 2).on(pq("-Qk", "--", "odd"), reply{out: "something else\n"})
	// garbled: the versions can't be read
	onPkg(f, "garbled", "1.0-1", "1.0-1", 2).on(pq("-Si", "--", "garbled"), reply{out: "Name : garbled\n"})
	o, out := rootOpts(t, f)
	if code := Run([]string{"repair", "healthy", "broken", "aur", "gone", "odd", "garbled", "broken"}, o); code != ExitOK {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if got := join(f.streams); !slices.Equal(got, []string{"/usr/bin/pacman -S --noconfirm -- broken"}) {
		t.Errorf("ran %q", got)
	}
	want := map[string]string{
		"healthy": "no missing files", "aur": "not in a sync repository",
		"gone": "not installed", "odd": "could not check its files",
		"garbled": "could not compare its installed and repo versions",
	}
	got := skippedNames(t, out)
	for k, v := range want {
		if got[k] != v {
			t.Errorf("skip %s = %q, want %q", k, got[k], v)
		}
	}
	for _, qa := range f.queries {
		if i := slices.Index(qa, "--"); i < 0 || i != len(qa)-2 {
			t.Errorf("probe without -- before the name: %q", qa)
		}
	}
}

func TestRepairNeverCausesAPartialUpgrade(t *testing.T) {
	const newer = "repo has a different version (installed 1.0-1, repo 1.1-1) — update the system first"
	t.Run("equal versions are reinstalled", func(t *testing.T) {
		f := newFake()
		onPkg(f, "a", "1:2.0.3-4", "1:2.0.3-4", 1)
		onPkg(f, "b", "1.0-1", "1.0-1", 3)
		onPlan(f, []string{"a", "b"}, "b 1.0-1", "a 1:2.0.3-4")
		o, out := rootOpts(t, f)
		if code := Run([]string{"repair", "a", "b"}, o); code != ExitOK {
			t.Fatalf("exit %d:\n%s", code, out)
		}
		if got := join(f.streams); !slices.Equal(got, []string{"/usr/bin/pacman -S --noconfirm -- a b"}) {
			t.Errorf("ran %q", got)
		}
		if sk := skippedNames(t, out); len(sk) != 0 {
			t.Errorf("skipped %v", sk)
		}
	})
	t.Run("newer in the repo is exit 3", func(t *testing.T) {
		f := newFake()
		onPkg(f, "a", "1.0-1", "1.1-1", 2)
		o, out := rootOpts(t, f)
		if code := Run([]string{"repair", "a"}, o); code != ExitStale {
			t.Fatalf("exit %d, want 3:\n%s", code, out)
		}
		if len(f.streams) != 0 {
			t.Errorf("ran %q", f.streams)
		}
		res := result(t, out)
		if msg, _ := res["error"].(string); !strings.Contains(msg, "update the system first") {
			t.Errorf("error %q does not explain", msg)
		}
		if res["child_exit"] != nil {
			t.Errorf("child_exit = %v, want null", res["child_exit"])
		}
		if got := skippedNames(t, out)["a"]; got != newer {
			t.Errorf("skip a = %q, want %q", got, newer)
		}
	})
	t.Run("older in the repo is not a downgrade", func(t *testing.T) {
		f := newFake()
		onPkg(f, "a", "1.1-1", "1.0-1", 2)
		o, out := rootOpts(t, f)
		if code := Run([]string{"repair", "a"}, o); code != ExitStale {
			t.Fatalf("exit %d, want 3:\n%s", code, out)
		}
		if len(f.streams) != 0 {
			t.Errorf("ran %q", f.streams)
		}
	})
	t.Run("the first repo decides", func(t *testing.T) {
		// pacman -S takes the first repo in pacman.conf order, as -Si lists it.
		f := newFake()
		onPkg(f, "a", "1.0-1", "", 2).on(pq("-Si", "--", "a"), reply{out: siInfo("cachyos-core-v4", "a", "1.1-1") + siInfo("core", "a", "1.0-1")})
		o, out := rootOpts(t, f)
		if code := Run([]string{"repair", "a"}, o); code != ExitStale {
			t.Fatalf("exit %d, want 3:\n%s", code, out)
		}
	})
	t.Run("mixed: only the equal ones are reinstalled", func(t *testing.T) {
		f := newFake()
		onPkg(f, "same", "2.0-1", "2.0-1", 1)
		onPlan(f, []string{"same"}, "same 2.0-1")
		onPkg(f, "newer", "1.0-1", "1.1-1", 1)
		onPkg(f, "healthy", "1.0-1", "1.1-1", 0)
		o, out := rootOpts(t, f)
		if code := Run([]string{"repair", "newer", "same", "healthy"}, o); code != ExitOK {
			t.Fatalf("exit %d:\n%s", code, out)
		}
		if got := join(f.streams); !slices.Equal(got, []string{"/usr/bin/pacman -S --noconfirm -- same"}) {
			t.Errorf("ran %q", got)
		}
		sk := skippedNames(t, out)
		if sk["newer"] != newer || sk["healthy"] != "no missing files" || len(sk) != 2 {
			t.Errorf("skipped %v", sk)
		}
	})
	t.Run("nothing broken stays exit 0", func(t *testing.T) {
		f := newFake()
		onPkg(f, "healthy", "1.0-1", "1.0-1", 0)
		o, out := rootOpts(t, f)
		if code := Run([]string{"repair", "healthy"}, o); code != ExitOK {
			t.Fatalf("exit %d:\n%s", code, out)
		}
	})
}

// TestRepairTransactionIsExactlyTheBrokenSet: what `pacman -S` would really
// do (`-Sp`) must be the broken packages at their installed versions and
// nothing else, or nothing is reinstalled.
func TestRepairTransactionIsExactlyTheBrokenSet(t *testing.T) {
	broken := func() *fakeRunner {
		f := newFake()
		onPkg(f, "a", "1.0-1", "1.0-1", 2)
		onPkg(f, "b", "2.0-1", "2.0-1", 1)
		return f
	}
	refused := func(t *testing.T, f *fakeRunner, want int) (string, map[string]string) {
		t.Helper()
		o, out := rootOpts(t, f)
		if code := Run([]string{"repair", "a", "b"}, o); code != want {
			t.Fatalf("exit %d, want %d:\n%s", code, want, out)
		}
		if len(f.streams) != 0 {
			t.Errorf("ran %q", f.streams)
		}
		msg, _ := result(t, out)["error"].(string)
		return msg, skippedNames(t, out)
	}
	t.Run("clean set is reinstalled", func(t *testing.T) {
		f := onPlan(broken(), []string{"a", "b"}, "a 1.0-1", "b 2.0-1")
		o, out := rootOpts(t, f)
		if code := Run([]string{"repair", "b", "a"}, o); code != ExitOK {
			t.Fatalf("exit %d:\n%s", code, out)
		}
		if got := join(f.streams); !slices.Equal(got, []string{"/usr/bin/pacman -S --noconfirm -- a b"}) {
			t.Errorf("ran %q", got)
		}
		if !slices.ContainsFunc(f.queries, func(q []string) bool {
			return slices.Equal(q, []string{Pacman, "-Sp", "--noconfirm", "--print-format", "%n %v", "--", "a", "b"})
		}) {
			t.Errorf("no -Sp plan was asked for: %q", f.queries)
		}
	})
	t.Run("an extra dependency is exit 3 and named", func(t *testing.T) {
		f := onPlan(broken(), []string{"a", "b"}, "libz 1.3-2", "a 1.0-1", "b 2.0-1", "glibc 2.41-1")
		msg, sk := refused(t, f, ExitStale)
		for _, extra := range []string{"libz 1.3-2", "glibc 2.41-1"} {
			if !strings.Contains(msg, extra) {
				t.Errorf("error %q does not name %s", msg, extra)
			}
			if !strings.Contains(sk["a"], extra) {
				t.Errorf("skip a = %q does not name %s", sk["a"], extra)
			}
		}
		if len(sk) != 2 || sk["b"] == "" {
			t.Errorf("skipped %v, want a and b", sk)
		}
	})
	t.Run("version drift is exit 3", func(t *testing.T) {
		// The sync db was refreshed between -Si and -Sp.
		f := onPlan(broken(), []string{"a", "b"}, "a 1.0-1", "b 2.1-1")
		msg, sk := refused(t, f, ExitStale)
		if !strings.Contains(msg, "b would change from 2.0-1 to 2.1-1") {
			t.Errorf("error %q does not explain the drift", msg)
		}
		if want := "pacman would install a different version (installed 2.0-1, repo 2.1-1) — update the system first"; sk["b"] != want {
			t.Errorf("skip b = %q, want %q", sk["b"], want)
		}
		if sk["a"] == "" {
			t.Errorf("a not reported as skipped: %v", sk)
		}
	})
	t.Run("a package missing from the transaction is exit 3", func(t *testing.T) {
		f := onPlan(broken(), []string{"a", "b"}, "a 1.0-1")
		if _, sk := refused(t, f, ExitStale); sk["b"] != "pacman would not reinstall it" {
			t.Errorf("skipped %v", sk)
		}
	})
	t.Run("odd -Sp output fails", func(t *testing.T) {
		for _, lines := range [][]string{
			{"a 1.0-1", ":: a is in IgnorePkg/IgnoreGroup. Install anyway? [Y/n]", "b 2.0-1"},
			{"a 1.0-1", "b 2.0-1", "b 2.0-1"},
			{"a 1.0-1", "b"},
		} {
			refused(t, onPlan(broken(), []string{"a", "b"}, lines...), ExitFailed)
		}
	})
	t.Run("-Sp failing fails", func(t *testing.T) {
		f := broken().on(repairPlanArgv([]string{"a", "b"}), reply{err: "error: failed to prepare transaction", exit: 1})
		if msg, _ := refused(t, f, ExitFailed); !strings.Contains(msg, "failed to prepare transaction") {
			t.Errorf("error %q", msg)
		}
	})
}

func TestRepairVersionParsing(t *testing.T) {
	for _, c := range []struct {
		out, want string
		ok        bool
	}{
		{"a 1.0-1\n", "1.0-1", true},
		{"a 1.0-1 extra\n", "", false},
		{"b 1.0-1\n", "", false},
		{"a 1.0-1\na 1.0-1\n", "", false},
		{"", "", false},
	} {
		if v, ok := installedVersion(c.out, "a"); v != c.want || ok != c.ok {
			t.Errorf("installedVersion(%q) = %q, %v", c.out, v, ok)
		}
	}
	for _, c := range []struct {
		out, want string
		ok        bool
	}{
		{siInfo("core", "a", "1:2.3-4"), "1:2.3-4", true},
		{"Name : a\nVersion :\n", "", false},
		{"Name : a\nVersion : 1 2\n", "", false},
		{"Name : a\n", "", false},
	} {
		if v, ok := syncVersion(c.out); v != c.want || ok != c.ok {
			t.Errorf("syncVersion(%q) = %q, %v", c.out, v, ok)
		}
	}
}

func TestRepairValidation(t *testing.T) {
	many := make([]string, maxNames+1)
	for i := range many {
		many[i] = "p"
	}
	for name, args := range map[string][]string{
		"none":       {},
		"too many":   many,
		"flag":       {"--overwrite=*"},
		"dash":       {"-foo"},
		"dot":        {".hidden"},
		"non-ascii":  {"pakkæ"},
		"slash repo": {"core/linux"},
		"space":      {"a b"},
		"empty":      {""},
		"too long":   {strings.Repeat("a", 129)},
		"one bad":    {"good", "b;ad"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFake()
			o, _ := rootOpts(t, f)
			if code := Run(append([]string{"repair"}, args...), o); code != ExitUsage {
				t.Errorf("exit %d, want 2", code)
			}
			if len(f.queries)+len(f.streams) != 0 {
				t.Errorf("ran commands for invalid input: %q %q", f.queries, f.streams)
			}
		})
	}
	for _, ok := range []string{"a", "lib32-glibc", "gtk+", "c++utilities", "python3.12", "@scope", "_x", "0ad", strings.Repeat("a", 128),
		"Linux", "MyTool-git", "QtCreator", // uppercase: makepkg allows it for local builds
	} {
		if !namePattern.MatchString(ok) {
			t.Errorf("%q should be a valid package name", ok)
		}
	}
}

// ---- privilege, install path, locks ----------------------------------------

func TestPrivilegeChecks(t *testing.T) {
	cases := map[string]func(o *Options){
		"not root":          func(o *Options) { o.Euid = 1000 },
		"no caller uid":     func(o *Options) { o.CallerUID = "" },
		"junk caller uid":   func(o *Options) { o.CallerUID = "1000;x" },
		"wrong path":        func(o *Options) { o.Exe = func() (string, error) { return "/tmp/archmaid-helper", nil } },
		"no install seam":   func(o *Options) { o.Exe = nil },
		"dev build as root": func(o *Options) { o.Dev = true },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFake()
			o, out := rootOpts(t, f)
			mutate(&o)
			if code := Run([]string{"check-files"}, o); code != ExitRefused {
				t.Fatalf("exit %d, want 4:\n%s", code, out)
			}
			if len(f.queries)+len(f.streams) != 0 {
				t.Errorf("ran commands without privilege")
			}
		})
	}
}

func TestDevBuildRunsAsUser(t *testing.T) {
	f := newFake()
	o, out := rootOpts(t, f)
	o.Dev, o.Euid, o.CallerUID, o.Exe = true, 1000, "", func() (string, error) { return "/home/x/archmaid-helper", nil }
	if code := Run([]string{"check-files"}, o); code != ExitOK {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if events(t, out)[0]["dev"] != true {
		t.Error("start event does not flag a dev build")
	}
}

func TestCheckInstall(t *testing.T) {
	exe := func() (string, error) { return InstallPath, nil }
	with := func(path string, st FileStat) func(string) (FileStat, error) {
		return func(p string) (FileStat, error) {
			if p == path {
				return st, nil
			}
			return rootStat(p)
		}
	}
	if err := CheckInstall(exe, rootStat); err != nil {
		t.Fatalf("a correct install is refused: %v", err)
	}
	bad := map[string]func(string) (FileStat, error){
		"binary user-owned":     with(InstallPath, FileStat{Uid: 1000, Mode: 0o755}),
		"binary group-writable": with(InstallPath, FileStat{Mode: 0o775}),
		"binary other-writable": with(InstallPath, FileStat{Mode: 0o757}),
		"binary setuid":         with(InstallPath, FileStat{Mode: fs.ModeSetuid | 0o755}),
		"binary is a symlink":   with(InstallPath, FileStat{Mode: fs.ModeSymlink | 0o777}),
		"binary is a dir":       with(InstallPath, FileStat{Mode: fs.ModeDir | 0o755}),
		"parent user-owned":     with("/usr/lib/archmaid", FileStat{Uid: 1000, Mode: fs.ModeDir | 0o755}),
		"parent group-writable": with("/usr/lib/archmaid", FileStat{Mode: fs.ModeDir | 0o775}),
		"grandparent writable":  with("/usr/lib", FileStat{Mode: fs.ModeDir | 0o777}),
		"root other-writable":   with("/", FileStat{Mode: fs.ModeDir | 0o757}),
		"parent symlink":        with("/usr/lib", FileStat{Mode: fs.ModeSymlink | 0o777}),
		"parent is not a dir":   with("/usr", FileStat{Mode: 0o755}),
		"stat fails":            func(string) (FileStat, error) { return FileStat{}, os.ErrPermission },
	}
	for name, st := range bad {
		t.Run(name, func(t *testing.T) {
			if err := CheckInstall(exe, st); err == nil {
				t.Error("accepted")
			}
		})
	}
	for _, p := range []string{"/usr/lib/archmaid/archmaid-helper (deleted)", "/usr/local/lib/archmaid/archmaid-helper", "archmaid-helper"} {
		if err := CheckInstall(func() (string, error) { return p, nil }, rootStat); err == nil {
			t.Errorf("accepted exe path %q", p)
		}
	}
}

func TestBusy(t *testing.T) {
	t.Run("own lock held", func(t *testing.T) {
		f := newFake()
		o, out := rootOpts(t, f)
		held, err := acquireLock(o.LockPath, o.PacmanDBLock, false)
		if err != nil {
			t.Fatal(err)
		}
		defer held()
		if code := Run([]string{"clean-tmp"}, o); code != ExitBusy {
			t.Fatalf("exit %d, want 5:\n%s", code, out)
		}
		if len(f.streams) != 0 {
			t.Errorf("ran %q while locked", f.streams)
		}
	})
	t.Run("lock released after run", func(t *testing.T) {
		o, _ := rootOpts(t, newFake())
		for range 2 {
			if code := Run([]string{"clean-tmp"}, o); code != ExitOK {
				t.Fatalf("exit %d: lock leaked from the previous run", code)
			}
		}
	})
	for verb, args := range map[string][]string{
		"paccache": {"3"}, "paccache-wipe": nil, "repair": {"x"},
		"remove-orphans": {hash0}, "full-cleanup": {"--keep", "3", "--journal", "2weeks"},
	} {
		t.Run("db.lck blocks "+verb, func(t *testing.T) {
			f := newFake()
			o, _ := rootOpts(t, f)
			if err := os.WriteFile(o.PacmanDBLock, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			if code := Run(append([]string{verb}, args...), o); code != ExitBusy {
				t.Fatalf("exit %d, want 5", code)
			}
			if len(f.queries)+len(f.streams) != 0 {
				t.Errorf("ran commands while pacman is busy")
			}
		})
	}
	t.Run("db.lck does not block check-files", func(t *testing.T) {
		o, _ := rootOpts(t, newFake())
		if err := os.WriteFile(o.PacmanDBLock, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if code := Run([]string{"check-files"}, o); code != ExitOK {
			t.Fatalf("exit %d", code)
		}
	})
}
