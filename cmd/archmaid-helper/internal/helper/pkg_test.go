package helper

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
)

// pkgSys is a fake pacman for the package verbs. Query answers the read-only
// calls from fixtures; it fails the test on any -S or -R that is not a
// -Si/-Sp/-Rs --print style read (the shim rule: no install or removal
// without -p). Stream is the root command: recorded, never executed.
type pkgSys struct {
	t *testing.T

	sync       map[string]bool   // name → in a sync repo
	installed  map[string]string // name → version
	updates    []string          // pacman -Qu lines
	planItems  []string          // -Sp lines: "repo name version"
	rel        map[string]string // item → "Replaces : x" / "Conflicts With : y" lines
	rmItems    []string          // -Rs --print lines: "name version"
	rmErr      string            // pacman refuses the removal with this (stderr)
	rmOut      string            // ... and this on stdout
	owners     string            // -Qqo output
	ownersExit int               // -Qqo exit (1: some path has no owner)
	baseDeps   string            // "Depends On" value of base

	queries, streams [][]string
	// onQuery runs before each query is answered.
	onQuery func(argv []string)
}

func newPkgSys(t *testing.T) *pkgSys {
	return &pkgSys{
		t:         t,
		sync:      map[string]bool{"foo": true, "bar": true, "libfoo": true},
		installed: map[string]string{"bash": "5.2-1", "glibc": "2.40-1", "libold": "1.0-1", "foo-old": "1-1"},
		planItems: []string{"extra foo 1.0-1", "extra libfoo 2.0-1"},
		rel:       map[string]string{},
		rmItems:   []string{"foo-old 1-1"},
		owners:    "linux-cachyos\n",
		baseDeps:  "filesystem glibc>=2.40",
	}
}

func (p *pkgSys) Query(argv []string) (string, string, int, error) {
	p.queries = append(p.queries, slices.Clone(argv))
	if p.onQuery != nil {
		p.onQuery(argv)
	}
	if len(argv) < 2 || argv[0] != Pacman {
		p.t.Fatalf("query is not pacman: %q", argv)
	}
	op := argv[1]
	args := argv[2:]
	i := slices.Index(args, "--")
	var names []string
	if i >= 0 {
		names = args[i+1:]
	}
	switch op {
	case "-Si":
		if len(names) == 1 && !strings.Contains(names[0], "/") {
			if p.sync[names[0]] {
				return "Name : " + names[0] + "\n", "", 0, nil
			}
			return "", "error: package '" + names[0] + "' was not found", 1, nil
		}
		var b strings.Builder
		for _, spec := range names {
			_, n, _ := strings.Cut(spec, "/")
			fmt.Fprintf(&b, "Repository      : extra\nName            : %s\nVersion         : 1\nDownload Size   : 1.50 MiB\nInstalled Size  : 4.00 KiB\n%s\nDescription     : x\n\n", n, p.rel[n])
		}
		return b.String(), "", 0, nil
	case "-Qu":
		if len(p.updates) == 0 {
			return "", "", 1, nil
		}
		return strings.Join(p.updates, "\n") + "\n", "", 0, nil
	case "-Q":
		var b strings.Builder
		for n, v := range p.installed {
			fmt.Fprintf(&b, "%s %s\n", n, v)
		}
		return b.String(), "", 0, nil
	case "-Qq":
		var b strings.Builder
		for n := range p.installed {
			b.WriteString(n + "\n")
		}
		return b.String(), "", 0, nil
	case "-Qqo":
		return p.owners, "", p.ownersExit, nil
	case "-Qi":
		if len(names) == 1 && names[0] == "base" {
			return "Name : base\nDepends On      : " + p.baseDeps + "\n", "", 0, nil
		}
		var b strings.Builder
		for _, n := range names {
			fmt.Fprintf(&b, "Name            : %s\nInstalled Size  : 2.00 MiB\n\n", n)
		}
		return b.String(), "", 0, nil
	case "-Sp":
		return strings.Join(p.planItems, "\n") + "\n", "", 0, nil
	case "-Rs", "-Rns":
		if !slices.Contains(args, "--print") {
			p.t.Fatalf("a query -R without --print: %q", argv)
		}
		if p.rmErr != "" {
			return p.rmOut, p.rmErr, 1, nil
		}
		return strings.Join(p.rmItems, "\n") + "\n", "", 0, nil
	}
	p.t.Fatalf("unexpected query (an -S or -R without -p?): %q", argv)
	return "", "", 2, nil
}

func (p *pkgSys) Stream(argv []string, line func(stream, text string)) (int, error) {
	p.streams = append(p.streams, slices.Clone(argv))
	if len(argv) < 2 || argv[0] != Pacman || !slices.Contains([]string{"-S", "-Rs", "-Rns"}, argv[1]) || slices.Contains(argv, "-Sp") {
		p.t.Fatalf("unexpected root command: %q", argv)
	}
	line("out", "done")
	return 0, nil
}

// run runs a verb and returns its exit code and events.
func runPkg(t *testing.T, p *pkgSys, args ...string) (int, []map[string]any) {
	t.Helper()
	o, out := rootOpts(t, p)
	o.KernelPkgbases = func() ([]string, error) { return []string{"/usr/lib/modules/6.1.0-1/pkgbase"}, nil }
	code := Run(args, o)
	return code, events(t, out)
}

func resultErr(evs []map[string]any) string {
	s, _ := evs[len(evs)-1]["error"].(string)
	return s
}

func planOf(t *testing.T, evs []map[string]any) map[string]any {
	t.Helper()
	ps := ofType(evs, EventPlan)
	if len(ps) != 1 {
		t.Fatalf("want one plan event, got %d", len(ps))
	}
	return ps[0]
}

func anyStrings(v any) []string {
	var r []string
	for _, x := range v.([]any) {
		r = append(r, x.(string))
	}
	return r
}

// TestPkgArgvPinned pins every argv of the package verbs, colour pair
// included: parsed calls say never, relayed ones always, and no option the
// plan forbids (-y -u -U -d -c --overwrite --noscriptlet) ever appears.
func TestPkgArgvPinned(t *testing.T) {
	n := []string{"a", "b"}
	for _, c := range []struct {
		name string
		got  []string
		want string
	}{
		{"plan install", installPlanArgv(n), "/usr/bin/pacman -Sp --needed --noconfirm --print-format %r %n %v --color never -- a b"},
		{"install", installPkgArgv(n, false), "/usr/bin/pacman -S --needed --noconfirm --color always -- a b"},
		{"install asdeps", installPkgArgv(n, true), "/usr/bin/pacman -S --needed --noconfirm --asdeps --color always -- a b"},
		{"plan remove", removePlanArgv(n, false), "/usr/bin/pacman -Rs --print --print-format %n %v --color never -- a b"},
		{"plan remove -n", removePlanArgv(n, true), "/usr/bin/pacman -Rs --print --print-format %n %v --color never -- a b"},
		{"remove", removePkgArgv(n, false), "/usr/bin/pacman -Rs --noconfirm --color always -- a b"},
		{"remove -n", removePkgArgv(n, true), "/usr/bin/pacman -Rns --noconfirm --color always -- a b"},
		{"updates", updatesArgv(), "/usr/bin/pacman -Qu --color never"},
		{"installed", allInstalledArgv(), "/usr/bin/pacman -Q --color never"},
		{"sync info", syncInfoArgv([]string{"extra/a"}), "/usr/bin/pacman -Si --color never -- extra/a"},
		{"local info", localInfoArgv(n), "/usr/bin/pacman -Qi --color never -- a b"},
	} {
		if g := strings.Join(c.got, " "); g != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, g, c.want)
		}
		for _, bad := range []string{"-y", "-Sy", "-u", "-U", "-d", "-dd", "-Rdd", "-Rc", "-c", "--overwrite", "--noscriptlet", "--config", "--dbpath", "--root"} {
			if slices.Contains(c.got, bad) {
				t.Errorf("%s holds forbidden %s", c.name, bad)
			}
		}
	}
}

func TestPkgNameTable(t *testing.T) {
	long := strings.Repeat("a", 129)
	var many []string
	for i := range MaxPkgNames + 1 {
		many = append(many, fmt.Sprintf("p%d", i))
	}
	bad := [][]string{
		{"-rf"}, {"--"}, {"--noconfirm"}, {"repo/pkg"}, {"./a.pkg.tar.zst"}, {"/tmp/a"}, {"http://x"},
		{"a>=1"}, {"a=1"}, {"a<1"}, {""}, {long}, {"a b"}, {"a\x00b"}, {"a\nb"}, {"a;b"}, {"$(x)"}, {"é"},
		{"foo", "foo"}, {"foo", "-bar"}, many, {},
	}
	for _, names := range bad {
		for _, verb := range [][]string{{"plan-install"}, {"plan-remove"}, {"plan-remove", "-n"}} {
			p := newPkgSys(t)
			code, evs := runPkg(t, p, slices.Concat(verb, names)...)
			if code != ExitUsage {
				t.Errorf("%v %q: exit %d, want 2 (%s)", verb, names, code, resultErr(evs))
			}
			if len(p.queries) != 0 || len(p.streams) != 0 {
				t.Errorf("%v %q ran something: %v %v", verb, names, p.queries, p.streams)
			}
		}
		h := PkgPlanHash("install", nil, nil)
		for _, verb := range [][]string{{"pkg-install", h}, {"pkg-remove", h}, {"pkg-remove", h, "-n"}} {
			p := newPkgSys(t)
			code, _ := runPkg(t, p, slices.Concat(verb, names)...)
			if code != ExitUsage || len(p.queries) != 0 || len(p.streams) != 0 {
				t.Errorf("%v %q: exit %d, ran %v %v", verb, names, code, p.queries, p.streams)
			}
		}
	}
	// A malformed hash, a missing hash, and -n anywhere but first.
	for _, args := range [][]string{
		{"pkg-install", "foo"}, {"pkg-install"}, {"pkg-install", "sha256:xyz", "foo"},
		{"pkg-remove", "sha256:abc", "foo"}, {"plan-remove", "foo", "-n"},
	} {
		p := newPkgSys(t)
		if code, _ := runPkg(t, p, args...); code != ExitUsage || len(p.queries) != 0 {
			t.Errorf("%v: exit %d, queries %v", args, code, p.queries)
		}
	}
	// 32 names is the most.
	p := newPkgSys(t)
	for _, n := range many[:MaxPkgNames] {
		p.sync[n] = true
	}
	if code, evs := runPkg(t, p, append([]string{"plan-install"}, many[:MaxPkgNames]...)...); code != ExitOK {
		t.Errorf("32 names: exit %d: %s", code, resultErr(evs))
	}
}

func TestPlanInstallHappy(t *testing.T) {
	p := newPkgSys(t)
	code, evs := runPkg(t, p, "plan-install", "foo")
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, resultErr(evs))
	}
	pl := planOf(t, evs)
	items := []string{"foo 1.0-1", "libfoo 2.0-1"}
	if !slices.Equal(anyStrings(pl["targets"]), []string{"foo"}) || !slices.Equal(anyStrings(pl["items"]), items) || pl["total"] != float64(2) {
		t.Errorf("plan = %v", pl)
	}
	if pl["hash"] != PkgPlanHash("install", []string{"foo"}, []string{"extra foo 1.0-1", "extra libfoo 2.0-1"}) {
		t.Errorf("hash %v", pl["hash"])
	}
	if len(p.streams) != 0 {
		t.Errorf("a plan verb ran root commands: %v", p.streams)
	}
	got := join(p.queries)
	if want := "/usr/bin/pacman -Si --color never -- extra/foo extra/libfoo"; !slices.Contains(got, want) {
		t.Errorf("no vetting call %q in %q", want, got)
	}
}

// A plan verb needs no root: it runs as a plain user.
func TestPlanVerbsNeedNoRoot(t *testing.T) {
	p := newPkgSys(t)
	o, out := rootOpts(t, p)
	o.Euid, o.CallerUID = 1000, ""
	if code := Run([]string{"plan-install", "foo"}, o); code != ExitOK {
		t.Fatalf("plan-install as user: exit %d: %s", code, resultErr(events(t, out)))
	}
	o, out = rootOpts(t, p)
	o.Euid, o.CallerUID = 1000, ""
	for _, v := range [][]string{{"pkg-install", PkgPlanHash("install", nil, nil), "foo"}, {"pkg-remove", PkgPlanHash("remove", nil, nil), "foo-old"}} {
		n := len(p.queries)
		if code := Run(v, o); code != ExitRefused {
			t.Errorf("%v as user: exit %d, want 4", v, code)
		}
		if len(p.queries) != n || len(p.streams) != 0 {
			t.Errorf("%v ran something as a user", v)
		}
		o, out = rootOpts(t, p)
		o.Euid, o.CallerUID = 1000, ""
	}
	_ = out
}

func TestPlanInstallRefusals(t *testing.T) {
	t.Run("not in a sync repo", func(t *testing.T) {
		p := newPkgSys(t)
		code, evs := runPkg(t, p, "plan-install", "foo", "nonesuch")
		if code != ExitUsage || !strings.Contains(resultErr(evs), `"nonesuch" is not in a sync repository`) {
			t.Errorf("exit %d: %s", code, resultErr(evs))
		}
		if len(ofType(evs, EventPlan)) != 0 {
			t.Error("a refused install emitted a plan")
		}
		wantReason(t, evs, ReasonNotFound, []string{"nonesuch"}, 0)
	})
	t.Run("a group or provider name is not a target", func(t *testing.T) {
		p := newPkgSys(t) // "gnome" and "java-runtime" are not package names in the DB
		for _, n := range []string{"gnome", "java-runtime"} {
			if code, _ := runPkg(t, p, "plan-install", n); code != ExitUsage {
				t.Errorf("%s: exit %d", n, code)
			}
		}
	})
	t.Run("updates pending", func(t *testing.T) {
		p := newPkgSys(t)
		p.updates = []string{"glibc 2.40-1 -> 2.41-1", "bash 5.2-1 -> 5.3-1"}
		code, evs := runPkg(t, p, "plan-install", "foo")
		if code != ExitStale || !strings.Contains(resultErr(evs), "2 update(s) are waiting: update the system first") {
			t.Errorf("exit %d: %s", code, resultErr(evs))
		}
		wantReason(t, evs, ReasonUpdatesPending, nil, 2)
		if slices.ContainsFunc(join(p.queries), func(s string) bool { return strings.Contains(s, "-Sp") }) {
			t.Error("planned after seeing pending updates")
		}
	})
	t.Run("upgrade", func(t *testing.T) {
		p := newPkgSys(t)
		p.installed["libfoo"] = "1.0-1" // the repo has 2.0-1
		code, evs := runPkg(t, p, "plan-install", "foo")
		if code != ExitStale || !strings.Contains(resultErr(evs), "libfoo is installed at 1.0-1 and the repo has 2.0-1: update the system first") {
			t.Errorf("exit %d: %s", code, resultErr(evs))
		}
		wantReason(t, evs, ReasonUpdatesPending, []string{"libfoo"}, 0)
	})
	t.Run("same version installed is not an upgrade", func(t *testing.T) {
		p := newPkgSys(t)
		p.installed["libfoo"] = "2.0-1"
		if code, evs := runPkg(t, p, "plan-install", "foo"); code != ExitOK {
			t.Errorf("exit %d: %s", code, resultErr(evs))
		}
	})
	for _, c := range []struct{ name, rel, want, other string }{
		{"replaces", "Replaces        : libold", "foo replaces the installed package libold", "libold"},
		{"conflicts", "Conflicts With  : zzz  foo-old<2", "foo conflicts with the installed package foo-old", "foo-old"},
		{"conflict continued on the next line", "Conflicts With  : zzz\n                  foo-old", "foo conflicts with the installed package foo-old", "foo-old"},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := newPkgSys(t)
			p.rel["foo"] = c.rel
			code, evs := runPkg(t, p, "plan-install", "foo")
			if code != ExitRefused || !strings.Contains(resultErr(evs), c.want) {
				t.Errorf("exit %d, want 4: %s", code, resultErr(evs))
			}
			// the whole sentence: it names what to install, and ends with a
			// full stop (the GUI puts its own sentence after it)
			if want := "remove " + c.other + " first, then install foo."; !strings.HasSuffix(resultErr(evs), want) {
				t.Errorf("error %q does not end with %q", resultErr(evs), want)
			}
			wantReason(t, evs, ReasonConflict, []string{"foo", c.other}, 0)
			if len(ofType(evs, EventPlan)) != 0 {
				t.Error("a refused install emitted a plan")
			}
		})
	}
	t.Run("replaces and conflicts that are not installed, or itself, pass", func(t *testing.T) {
		p := newPkgSys(t)
		p.rel["foo"] = "Replaces        : nothere\nConflicts With  : foo  nothere2>=1"
		p.rel["libfoo"] = "Replaces        : None\nConflicts With  : None"
		if code, evs := runPkg(t, p, "plan-install", "foo"); code != ExitOK {
			t.Errorf("exit %d: %s", code, resultErr(evs))
		}
	})
	t.Run("a dependency that conflicts is refused too", func(t *testing.T) {
		p := newPkgSys(t)
		p.rel["libfoo"] = "Conflicts With  : libold"
		if code, evs := runPkg(t, p, "plan-install", "foo"); code != ExitRefused || !strings.Contains(resultErr(evs), "libfoo conflicts with the installed package libold") {
			t.Errorf("exit %d: %s", code, resultErr(evs))
		}
	})
	t.Run("junk from -Sp", func(t *testing.T) {
		for _, l := range []string{"extra foo", "foo 1.0-1", "-bad foo 1-1", "extra foo 1-1 x", "extra -foo 1-1"} {
			p := newPkgSys(t)
			p.planItems = []string{l}
			if code, _ := runPkg(t, p, "plan-install", "foo"); code != ExitFailed {
				t.Errorf("%q: exit %d, want 1", l, code)
			}
		}
	})
}

func TestPkgPlanHash(t *testing.T) {
	a := PkgPlanHash("install", []string{"b", "a"}, []string{"x 1", "y 2"})
	if b := PkgPlanHash("install", []string{"a", "b"}, []string{"y 2", "x 1"}); a != b {
		t.Errorf("order changed the hash: %s %s", a, b)
	}
	if !hashPattern.MatchString(a) {
		t.Errorf("hash %q is not a hash", a)
	}
	for name, h := range map[string]string{
		"mode":   PkgPlanHash("remove", []string{"a", "b"}, []string{"x 1", "y 2"}),
		"mode n": PkgPlanHash("remove-n", []string{"a", "b"}, []string{"x 1", "y 2"}),
		"target": PkgPlanHash("install", []string{"a"}, []string{"x 1", "y 2"}),
		"item":   PkgPlanHash("install", []string{"a", "b"}, []string{"x 1", "y 3"}),
		"extra":  PkgPlanHash("install", []string{"a", "b"}, []string{"x 1", "y 2", "z 1"}),
		"orphan": PlanHash([]string{"a", "b"}, []string{"x 1", "y 2"}),
	} {
		if h == a {
			t.Errorf("a different %s hashes the same", name)
		}
	}
	// the hash is order independent end to end
	p1, p2 := newPkgSys(t), newPkgSys(t)
	p2.planItems = []string{"extra libfoo 2.0-1", "extra foo 1.0-1"}
	_, e1 := runPkg(t, p1, "plan-install", "foo", "bar")
	_, e2 := runPkg(t, p2, "plan-install", "bar", "foo")
	if planOf(t, e1)["hash"] != planOf(t, e2)["hash"] {
		t.Error("name or pacman order changed the plan hash")
	}
}

// confirmedHash is the hash a front end would hold after plan-install.
func confirmedHash(t *testing.T, p *pkgSys, args ...string) string {
	t.Helper()
	code, evs := runPkg(t, p, args...)
	if code != ExitOK {
		t.Fatalf("%v: exit %d: %s", args, code, resultErr(evs))
	}
	return planOf(t, evs)["hash"].(string)
}

func TestPkgInstallRuns(t *testing.T) {
	p := newPkgSys(t)
	h := confirmedHash(t, p, "plan-install", "foo", "bar")
	p.queries = nil
	code, evs := runPkg(t, p, "pkg-install", h, "foo", "bar")
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, resultErr(evs))
	}
	if got := join(p.streams); !slices.Equal(got, []string{"/usr/bin/pacman -S --needed --noconfirm --color always -- bar foo"}) {
		t.Errorf("streams %q", got)
	}
	if len(ofType(evs, EventLine)) == 0 {
		t.Error("pacman's output was not relayed")
	}
}

func TestPkgInstallStale(t *testing.T) {
	p := newPkgSys(t)
	h := confirmedHash(t, p, "plan-install", "foo")
	p.planItems = append(p.planItems, "extra surprise 9-1") // the world moved on
	p.queries = nil
	code, evs := runPkg(t, p, "pkg-install", h, "foo")
	if code != ExitStale {
		t.Fatalf("exit %d, want 3", code)
	}
	if len(p.streams) != 0 {
		t.Fatalf("a stale plan ran %v", p.streams)
	}
	wantReason(t, evs, ReasonStale, nil, 0)
	pl := planOf(t, evs)
	if pl["hash"] == h || !slices.Contains(anyStrings(pl["items"]), "surprise 9-1") {
		t.Errorf("the new plan was not emitted: %v", pl)
	}
}

func TestPkgInstallVetsAgainUnderLock(t *testing.T) {
	p := newPkgSys(t)
	h := confirmedHash(t, p, "plan-install", "foo")
	p.updates = []string{"glibc 2.40-1 -> 2.41-1"} // an update landed after the confirm
	code, evs := runPkg(t, p, "pkg-install", h, "foo")
	if code != ExitStale || len(p.streams) != 0 {
		t.Errorf("exit %d, streams %v", code, p.streams)
	}
	wantReason(t, evs, ReasonUpdatesPending, nil, 1)
}

func TestPkgDBLockBeforeAndAfter(t *testing.T) {
	for _, verb := range []string{"pkg-install", "pkg-remove"} {
		args := []string{"foo"}
		if verb == "pkg-remove" {
			args = []string{"foo-old"}
		}
		plan := "plan-install"
		if verb == "pkg-remove" {
			plan = "plan-remove"
		}
		t.Run(verb+" db.lck before", func(t *testing.T) {
			p := newPkgSys(t)
			h := confirmedHash(t, p, append([]string{plan}, args...)...)
			p.queries = nil
			o, out := rootOpts(t, p)
			if err := os.WriteFile(o.PacmanDBLock, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			if code := Run(append([]string{verb, h}, args...), o); code != ExitBusy {
				t.Errorf("exit %d, want 5", code)
			}
			wantReason(t, events(t, out), ReasonBusy, nil, 0)
			if len(p.queries) != 0 || len(p.streams) != 0 {
				t.Errorf("ran %v %v with db.lck present", p.queries, p.streams)
			}
		})
		t.Run(verb+" db.lck after the recompute", func(t *testing.T) {
			p := newPkgSys(t)
			h := confirmedHash(t, p, append([]string{plan}, args...)...)
			p.queries = nil
			o, out := rootOpts(t, p)
			o.KernelPkgbases = func() ([]string, error) { return nil, nil }
			p.onQuery = func(argv []string) {
				if argv[1] == "-Sp" || strings.HasPrefix(argv[1], "-R") { // pacman starts during the recompute
					os.WriteFile(o.PacmanDBLock, nil, 0o600)
				}
			}
			if code := Run(append([]string{verb, h}, args...), o); code != ExitBusy {
				t.Errorf("exit %d, want 5", code)
			}
			wantReason(t, events(t, out), ReasonBusy, nil, 0)
			if len(p.streams) != 0 {
				t.Errorf("ran %v after db.lck appeared", p.streams)
			}
		})
	}
}

func TestPkgInstallNothingToDo(t *testing.T) {
	p := newPkgSys(t)
	p.planItems = nil // everything is installed and current
	h := confirmedHash(t, p, "plan-install", "foo")
	code, _ := runPkg(t, p, "pkg-install", h, "foo")
	if code != ExitOK || len(p.streams) != 0 {
		t.Errorf("exit %d, streams %v", code, p.streams)
	}
}

func TestPlanRemove(t *testing.T) {
	t.Run("-Rs", func(t *testing.T) {
		p := newPkgSys(t)
		p.rmItems = []string{"foo-old 1-1", "libold 1.0-1"}
		code, evs := runPkg(t, p, "plan-remove", "foo-old")
		if code != ExitOK {
			t.Fatalf("exit %d: %s", code, resultErr(evs))
		}
		pl := planOf(t, evs)
		if pl["hash"] != PkgPlanHash("remove", []string{"foo-old"}, p.rmItems) || pl["total"] != float64(2) {
			t.Errorf("plan %v", pl)
		}
		if !slices.Contains(join(p.queries), "/usr/bin/pacman -Rs --print --print-format %n %v --color never -- foo-old") {
			t.Errorf("queries %q", join(p.queries))
		}
	})
	t.Run("-n plans with -Rs (pacman refuses -Rns --print) but keeps the -Rns hash", func(t *testing.T) {
		p := newPkgSys(t)
		code, evs := runPkg(t, p, "plan-remove", "-n", "foo-old")
		if code != ExitOK {
			t.Fatalf("exit %d: %s", code, resultErr(evs))
		}
		if planOf(t, evs)["hash"] != PkgPlanHash("remove-n", []string{"foo-old"}, p.rmItems) {
			t.Error("hash is not the -Rns one")
		}
		q := join(p.queries)
		if !slices.Contains(q, "/usr/bin/pacman -Rs --print --print-format %n %v --color never -- foo-old") {
			t.Errorf("queries %q", q)
		}
		for _, l := range q {
			if strings.Contains(l, "-Rns") {
				t.Errorf("a plan query used -Rns: %q", l)
			}
		}
	})
	t.Run("not installed", func(t *testing.T) {
		p := newPkgSys(t)
		code, evs := runPkg(t, p, "plan-remove", "ghost")
		if code != ExitUsage || !strings.Contains(resultErr(evs), `"ghost" is not installed`) {
			t.Errorf("exit %d: %s", code, resultErr(evs))
		}
		wantReason(t, evs, ReasonNotInstalled, []string{"ghost"}, 0)
	})
	// The texts are pacman's own: the msgids of /usr/share/locale/*/
	// LC_MESSAGES/pacman.mo ("removing %s breaks dependency '%s' required by
	// %s", "failed to prepare transaction (%s)", "invalid option: '%s' and
	// '%s' may not be used together") and libalpm.mo ("could not satisfy
	// dependencies"); pacman prints the breaks lines to stdout after ":: ".
	t.Run("pacman refuses on dependencies: required-by with the dependents", func(t *testing.T) {
		p := newPkgSys(t)
		p.rmErr = "error: failed to prepare transaction (could not satisfy dependencies)"
		p.rmOut = ":: removing libold breaks dependency 'libold' required by foo-new\n:: removing libold breaks dependency 'libold.so=1-64' required by bar\n:: removing libold breaks dependency 'libold' required by foo-new\n"
		code, evs := runPkg(t, p, "plan-remove", "foo-old")
		if code != ExitRefused || !strings.Contains(resultErr(evs), "still needed by foo-new, bar") {
			t.Errorf("exit %d: %s", code, resultErr(evs))
		}
		wantReason(t, evs, ReasonRequiredBy, []string{"foo-new", "bar"}, 0)
	})
	t.Run("a refusal with no parsable dependents says so, names empty", func(t *testing.T) {
		p := newPkgSys(t)
		p.rmErr = "error: failed to prepare transaction (could not satisfy dependencies)"
		code, evs := runPkg(t, p, "plan-remove", "foo-old")
		if code != ExitRefused || !strings.Contains(resultErr(evs), "does not say which packages") {
			t.Errorf("exit %d: %s", code, resultErr(evs))
		}
		wantReason(t, evs, ReasonRequiredBy, nil, 0)
	})
	t.Run("any other pacman error is a plain failure, not required-by", func(t *testing.T) {
		for _, e := range []string{
			"error: invalid option: '--nosave' and '--print' may not be used together",
			"error: failed to init transaction (unable to lock database)",
			"error: target not found: foo-old",
		} {
			p := newPkgSys(t)
			p.rmErr = e
			code, evs := runPkg(t, p, "plan-remove", "foo-old")
			if code != ExitFailed || !strings.Contains(resultErr(evs), strings.TrimPrefix(e, "error: ")) {
				t.Errorf("%q: exit %d: %s", e, code, resultErr(evs))
			}
			wantReason(t, evs, "", nil, 0)
		}
	})
}

func TestProtectedRemoval(t *testing.T) {
	// B4 from the live check: pacman's own refusal (others need the target)
	// used to come first and hide the protected list. Now the targets are
	// vetted against the list before pacman is asked about the cascade.
	t.Run("a protected target is refused before pacman's cascade is asked", func(t *testing.T) {
		for _, n := range []string{"sudo", "linux-cachyos", "glibc", "limine", "mkinitcpio"} {
			p := newPkgSys(t)
			p.installed[n] = "1-1"
			p.rmErr = "error: failed to prepare transaction (could not satisfy dependencies)"
			p.rmOut = ":: removing " + n + " breaks dependency '" + n + "' required by archmaid-git\n"
			code, evs := runPkg(t, p, "plan-remove", n)
			if code != ExitRefused {
				t.Errorf("%s: exit %d: %s", n, code, resultErr(evs))
			}
			wantReason(t, evs, ReasonProtected, []string{n}, 0)
			if want := n + " is protected ("; !strings.HasPrefix(resultErr(evs), want) || !strings.HasSuffix(resultErr(evs), "and is never removed here.") {
				t.Errorf("%s: error %q", n, resultErr(evs))
			}
			for _, q := range join(p.queries) {
				if strings.Contains(q, "-Rs") || strings.Contains(q, "-Rns") {
					t.Errorf("%s: pacman was asked for the cascade: %q", n, q)
				}
			}
		}
	})
	t.Run("an unprotected package pacman refuses: required-by with the dependents", func(t *testing.T) {
		p := newPkgSys(t)
		p.installed["pacman-contrib"] = "1-1"
		p.rmErr = "error: failed to prepare transaction (could not satisfy dependencies)"
		p.rmOut = ":: removing pacman-contrib breaks dependency 'pacman-contrib' required by some-tool\n"
		code, evs := runPkg(t, p, "plan-remove", "pacman-contrib")
		if code != ExitRefused {
			t.Errorf("exit %d: %s", code, resultErr(evs))
		}
		wantReason(t, evs, ReasonRequiredBy, []string{"some-tool"}, 0)
	})
	t.Run("the running kernel's owner is protected although some modules dir has no owner", func(t *testing.T) {
		p := newPkgSys(t)
		p.installed["linux-cachyos"] = "6.1-1"
		p.owners, p.ownersExit = "linux-cachyos\n", 1 // pacman -Qqo: exit 1, the owned ones still printed
		code, evs := runPkg(t, p, "plan-remove", "linux-cachyos")
		if code != ExitRefused {
			t.Errorf("exit %d: %s", code, resultErr(evs))
		}
		wantReason(t, evs, ReasonProtected, []string{"linux-cachyos"}, 0)
		if !strings.Contains(resultErr(evs), "(an installed kernel)") {
			t.Errorf("error %q", resultErr(evs))
		}
	})
	t.Run("the cascade message ends with a full stop", func(t *testing.T) {
		p := newPkgSys(t)
		p.rmItems = []string{"foo-old 1-1", "bash 5.2-1"}
		_, evs := runPkg(t, p, "plan-remove", "foo-old")
		if !strings.HasSuffix(resultErr(evs), "which is protected (the system shell).") {
			t.Errorf("error %q", resultErr(evs))
		}
	})
	t.Run("static targets", func(t *testing.T) {
		for _, n := range []string{"base", "filesystem", "glibc", "bash", "coreutils", "systemd", "pacman", "sudo", "polkit", "archlinux-keyring", "grub", "limine", "mkinitcpio", "linux"} {
			p := newPkgSys(t)
			p.installed[n] = "1-1"
			p.rmItems = []string{n + " 1-1"}
			code, evs := runPkg(t, p, "plan-remove", n)
			if code != ExitRefused || !strings.Contains(resultErr(evs), n+" is protected") {
				t.Errorf("%s: exit %d: %s", n, code, resultErr(evs))
			}
			wantReason(t, evs, ReasonProtected, []string{n}, 0)
			if len(ofType(evs, EventPlan)) != 0 {
				t.Errorf("%s: a refused removal emitted a plan", n)
			}
		}
	})
	t.Run("archmaid prefix", func(t *testing.T) {
		for _, n := range []string{"archmaid", "archmaid-git", "archmaid-gui"} {
			p := newPkgSys(t)
			p.installed[n] = "1-1"
			p.rmItems = []string{n + " 1-1"}
			if code, _ := runPkg(t, p, "plan-remove", n); code != ExitRefused {
				t.Errorf("%s: exit %d", n, code)
			}
		}
	})
	t.Run("protected only in the cascade", func(t *testing.T) {
		p := newPkgSys(t)
		p.rmItems = []string{"foo-old 1-1", "bash 5.2-1"}
		code, evs := runPkg(t, p, "plan-remove", "foo-old")
		if code != ExitRefused || !strings.Contains(resultErr(evs), "would also remove bash, which is protected") {
			t.Errorf("exit %d: %s", code, resultErr(evs))
		}
		wantReason(t, evs, ReasonProtected, []string{"bash"}, 0)
	})
	t.Run("an installed kernel, found by its pkgbase", func(t *testing.T) {
		p := newPkgSys(t)
		p.installed["linux-cachyos"] = "6.1-1"
		p.rmItems = []string{"linux-cachyos 6.1-1"}
		code, evs := runPkg(t, p, "plan-remove", "linux-cachyos")
		if code != ExitRefused || !strings.Contains(resultErr(evs), "linux-cachyos is protected (an installed kernel)") {
			t.Errorf("exit %d: %s", code, resultErr(evs))
		}
		if !slices.Contains(join(p.queries), "/usr/bin/pacman -Qqo --color never -- /usr/lib/modules/6.1.0-1/pkgbase") {
			t.Errorf("the kernel owner was not asked: %q", join(p.queries))
		}
	})
	t.Run("what base depends on", func(t *testing.T) {
		p := newPkgSys(t)
		p.baseDeps = "filesystem glibc>=2.40 zlib-ng"
		p.installed["zlib-ng"] = "1-1"
		p.rmItems = []string{"zlib-ng 1-1"}
		code, evs := runPkg(t, p, "plan-remove", "zlib-ng")
		if code != ExitRefused || !strings.Contains(resultErr(evs), "zlib-ng is protected (required by the base system)") {
			t.Errorf("exit %d: %s", code, resultErr(evs))
		}
	})
	t.Run("pkg-remove enforces it too, before any root command", func(t *testing.T) {
		p := newPkgSys(t)
		h := PkgPlanHash("remove", []string{"bash"}, []string{"bash 5.2-1"})
		p.rmItems = []string{"bash 5.2-1"}
		code, _ := runPkg(t, p, "pkg-remove", h, "bash")
		if code != ExitRefused || len(p.streams) != 0 {
			t.Errorf("exit %d, streams %v", code, p.streams)
		}
	})
	t.Run("a failing kernel lookup refuses", func(t *testing.T) {
		p := newPkgSys(t)
		o, out := rootOpts(t, p)
		o.KernelPkgbases = func() ([]string, error) { return nil, fmt.Errorf("boom") }
		if code := Run([]string{"plan-remove", "foo-old"}, o); code != ExitFailed {
			t.Errorf("exit %d, want 1: %s", code, resultErr(events(t, out)))
		}
	})
	t.Run("an unprotected package passes", func(t *testing.T) {
		p := newPkgSys(t)
		if code, evs := runPkg(t, p, "plan-remove", "foo-old"); code != ExitOK {
			t.Errorf("exit %d: %s", code, resultErr(evs))
		}
	})
}

func TestPkgRemoveRuns(t *testing.T) {
	for _, c := range []struct {
		flag []string
		mode string
		want string
	}{
		{nil, "remove", "/usr/bin/pacman -Rs --noconfirm --color always -- foo-old libold"},
		{[]string{"-n"}, "remove-n", "/usr/bin/pacman -Rns --noconfirm --color always -- foo-old libold"},
	} {
		t.Run(c.mode, func(t *testing.T) {
			p := newPkgSys(t)
			plan := append(append([]string{"plan-remove"}, c.flag...), "libold", "foo-old")
			h := confirmedHash(t, p, plan...)
			run := append(append([]string{"pkg-remove", h}, c.flag...), "libold", "foo-old")
			if code, evs := runPkg(t, p, run...); code != ExitOK {
				t.Fatalf("exit %d: %s", code, resultErr(evs))
			}
			if got := join(p.streams); !slices.Equal(got, []string{c.want}) {
				t.Errorf("streams %q", got)
			}
		})
	}
	t.Run("a hash for -Rs cannot be spent on -Rns", func(t *testing.T) {
		p := newPkgSys(t)
		h := confirmedHash(t, p, "plan-remove", "foo-old")
		code, _ := runPkg(t, p, "pkg-remove", h, "-n", "foo-old")
		if code != ExitStale || len(p.streams) != 0 {
			t.Errorf("exit %d, streams %v", code, p.streams)
		}
	})
	t.Run("stale", func(t *testing.T) {
		p := newPkgSys(t)
		h := confirmedHash(t, p, "plan-remove", "foo-old")
		p.rmItems = append(p.rmItems, "libold 1.0-1")
		code, evs := runPkg(t, p, "pkg-remove", h, "foo-old")
		if code != ExitStale || len(p.streams) != 0 {
			t.Fatalf("exit %d, streams %v", code, p.streams)
		}
		if pl := planOf(t, evs); pl["hash"] == h || len(anyStrings(pl["items"])) != 2 {
			t.Errorf("new plan not emitted: %v", pl)
		}
	})
}

// A hash for an install cannot be spent on a remove, and the names are part
// of it.
func TestPkgHashBindsModeAndNames(t *testing.T) {
	p := newPkgSys(t)
	h := confirmedHash(t, p, "plan-install", "foo")
	if code, _ := runPkg(t, p, "pkg-install", h, "bar"); code != ExitStale || len(p.streams) != 0 {
		t.Errorf("other names: exit %d, streams %v", code, p.streams)
	}
	if code, _ := runPkg(t, p, "pkg-remove", h, "foo-old"); code != ExitStale || len(p.streams) != 0 {
		t.Errorf("install hash on remove: exit %d, streams %v", code, p.streams)
	}
}

// Every query the package verbs make is plain (no escape can be parsed)
// and every pacman carries a --color pair.
func TestPkgVerbsColourPairs(t *testing.T) {
	p := newPkgSys(t)
	h := confirmedHash(t, p, "plan-install", "foo")
	runPkg(t, p, "pkg-install", h, "foo")
	h = confirmedHash(t, p, "plan-remove", "-n", "foo-old")
	runPkg(t, p, "pkg-remove", h, "-n", "foo-old")
	for _, q := range p.queries {
		if i := slices.Index(q, "--color"); i < 0 || q[i+1] != "never" {
			t.Errorf("query without --color never: %q", q)
		}
	}
	if len(p.streams) != 2 {
		t.Fatalf("streams %v", p.streams)
	}
	for _, s := range p.streams {
		if i := slices.Index(s, "--color"); i < 0 || s[i+1] != "always" {
			t.Errorf("relayed command without --color always: %q", s)
		}
		if i, j := slices.Index(s, "--color"), slices.Index(s, "--"); i > j {
			t.Errorf("--color after --: %q", s)
		}
	}
}

// wantReason pins the result event's machine-readable refusal: the reason,
// the names (nil: none) and the pending count (0: none).
func wantReason(t *testing.T, evs []map[string]any, reason string, names []string, count int) {
	t.Helper()
	r := evs[len(evs)-1]
	if got, _ := r["reason"].(string); got != reason {
		t.Errorf("reason = %q, want %q (error %v)", got, reason, r["error"])
	}
	var got []string
	if v, ok := r["names"]; ok {
		got = anyStrings(v)
	}
	if !slices.Equal(got, names) {
		t.Errorf("names = %q, want %q", got, names)
	}
	if c, _ := r["count"].(float64); int(c) != count {
		t.Errorf("count = %v, want %d", r["count"], count)
	}
}

// A request that is malformed says bad-request, and the other verbs' refusals
// carry no reason at all.
func TestBadRequestReason(t *testing.T) {
	for _, args := range [][]string{
		{"plan-install", "-x"}, {"plan-remove"}, {"pkg-install", "nohash", "foo"}, {"plan-install", "foo", "foo"},
		{"plan-install", "-n", "foo"}, {"plan-remove", "--asdeps", "foo"}, {"plan-install", "foo", "--asdeps"},
	} {
		p := newPkgSys(t)
		code, evs := runPkg(t, p, args...)
		if code != ExitUsage {
			t.Errorf("%v: exit %d", args, code)
		}
		wantReason(t, evs, ReasonBadRequest, nil, 0)
	}
	p := newPkgSys(t)
	_, evs := runPkg(t, p, "paccache", "x")
	if _, ok := evs[len(evs)-1]["reason"]; ok {
		t.Error("a non-package verb's result carries a reason")
	}
}

// --asdeps: right after the verb (plan) or after H (root), part of the hash,
// and in the -S argv.
func TestAsDeps(t *testing.T) {
	p := newPkgSys(t)
	code, evs := runPkg(t, p, "plan-install", "--asdeps", "foo")
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, resultErr(evs))
	}
	pl := planOf(t, evs)
	items := []string{"extra foo 1.0-1", "extra libfoo 2.0-1"} // the hash input: repo name version
	if pl["asdeps"] != true || pl["mode"] != "install" {
		t.Errorf("plan %v", pl)
	}
	h := pl["hash"].(string)
	if h != PkgPlanHash("install-asdeps", []string{"foo"}, items) || h == PkgPlanHash("install", []string{"foo"}, items) {
		t.Errorf("--asdeps is not in the hash: %s", h)
	}
	// the plain hash cannot be spent on --asdeps and the other way round
	plain := confirmedHash(t, p, "plan-install", "foo")
	if code, _ := runPkg(t, p, "pkg-install", plain, "--asdeps", "foo"); code != ExitStale || len(p.streams) != 0 {
		t.Errorf("plain hash spent on --asdeps: exit %d, streams %v", code, p.streams)
	}
	if code, evs := runPkg(t, p, "pkg-install", h, "--asdeps", "foo"); code != ExitOK {
		t.Fatalf("exit %d: %s", code, resultErr(evs))
	}
	if got := join(p.streams); !slices.Equal(got, []string{"/usr/bin/pacman -S --needed --noconfirm --asdeps --color always -- foo"}) {
		t.Errorf("streams %q", got)
	}
	// the planning call does not carry it
	for _, q := range p.queries {
		if slices.Contains(q, "--asdeps") {
			t.Errorf("a query holds --asdeps: %q", q)
		}
	}
}

// The plan event's additive fields (packages-contract.md section 3).
func TestPlanEventFields(t *testing.T) {
	p := newPkgSys(t)
	_, evs := runPkg(t, p, "plan-install", "foo")
	pl := planOf(t, evs)
	rows := pl["rows"].([]any)
	if pl["mode"] != "install" || len(rows) != 2 || pl["total"] != float64(2) {
		t.Fatalf("plan %v", pl)
	}
	if _, ok := pl["asdeps"]; ok {
		t.Error("asdeps emitted when false")
	}
	r0 := rows[0].(map[string]any)
	want := map[string]any{"name": "foo", "version": "1.0-1", "old": "", "repo": "extra", "target": true, "dl": float64(1.5 * 1048576), "inst": float64(4096)}
	for k, v := range want {
		if r0[k] != v {
			t.Errorf("row[0][%s] = %v, want %v", k, r0[k], v)
		}
	}
	if r1 := rows[1].(map[string]any); r1["name"] != "libfoo" || r1["target"] != false {
		t.Errorf("row[1] = %v", r1)
	}

	p = newPkgSys(t)
	_, evs = runPkg(t, p, "plan-remove", "-n", "foo-old")
	pl = planOf(t, evs)
	r0 = pl["rows"].([]any)[0].(map[string]any)
	if pl["mode"] != "remove" || pl["with_config"] != true || r0["repo"] != "" || r0["target"] != true || r0["inst"] != float64(2<<20) {
		t.Errorf("remove plan %v", pl)
	}

	// a big plan carries a warning
	p = newPkgSys(t)
	p.planItems = nil
	for i := range 35 {
		p.planItems = append(p.planItems, fmt.Sprintf("extra d%02d 1-1", i))
	}
	p.planItems = append(p.planItems, "extra foo 1-1")
	_, evs = runPkg(t, p, "plan-install", "foo")
	if w, ok := planOf(t, evs)["warnings"]; !ok || len(anyStrings(w)) != 1 {
		t.Errorf("no warning on a 36-item plan: %v", planOf(t, evs))
	}

	// plan-orphans stays as it was
	o := newFake().on(orphansArgv(), reply{out: "a\n"}).on(cascadeArgv([]string{"a"}), reply{out: "a 1-1\n"})
	oo, out := rootOpts(t, o)
	Run([]string{"plan-orphans"}, oo)
	opl := planOf(t, events(t, out))
	for _, k := range []string{"mode", "rows", "warnings", "with_config", "asdeps"} {
		if _, ok := opl[k]; ok {
			t.Errorf("plan-orphans emits %s", k)
		}
	}
}

// The hashes are pinned to their documented text (the client mirrors them):
// both modes hash "name version" items; the repo is not hashed.
func TestPkgHashGolden(t *testing.T) {
	sum := func(text string) string {
		d := sha256.Sum256([]byte(text))
		return "sha256:" + hex.EncodeToString(d[:])
	}
	if got, want := PkgPlanHash("remove-n", []string{"b", "a"}, []string{"y 2", "x 1"}),
		sum("archmaid pkg plan v1\nmode remove-n\ntarget a\ntarget b\nitem x 1\nitem y 2\n"); got != want {
		t.Errorf("remove hash %s, want %s", got, want)
	}
	if got, want := PkgPlanHash("install-asdeps", []string{"a", "b"}, []string{"extra b 1-1", "core a 2-1"}),
		sum("archmaid pkg plan v1\nmode install-asdeps\ntarget a\ntarget b\nitem core a 2-1\nitem extra b 1-1\n"); got != want {
		t.Errorf("install hash %s, want %s", got, want)
	}
}

// The plan event's invariants (the client relies on them): total ==
// len(items) == len(rows), items[i] == rows[i].name + " " + rows[i].version,
// install items without the repo, the repo in rows.
func TestPlanEventInvariants(t *testing.T) {
	check := func(name string, evs []map[string]any) {
		pl := planOf(t, evs)
		items, rows := anyStrings(pl["items"]), pl["rows"].([]any)
		if int(pl["total"].(float64)) != len(items) || len(items) != len(rows) {
			t.Errorf("%s: total %v, %d items, %d rows", name, pl["total"], len(items), len(rows))
		}
		for i, r := range rows {
			m := r.(map[string]any)
			if items[i] != m["name"].(string)+" "+m["version"].(string) {
				t.Errorf("%s: items[%d] = %q, row %v", name, i, items[i], m)
			}
			for _, k := range []string{"name", "version", "old", "repo", "target", "dl", "inst"} {
				if _, ok := m[k]; !ok {
					t.Errorf("%s: row %d has no %s", name, i, k)
				}
			}
		}
		for _, k := range []string{"mode", "targets", "hash"} {
			if _, ok := pl[k]; !ok {
				t.Errorf("%s: no %s", name, k)
			}
		}
	}
	p := newPkgSys(t)
	_, evs := runPkg(t, p, "plan-install", "foo")
	check("install", evs)
	if r := planOf(t, evs)["rows"].([]any)[0].(map[string]any); r["repo"] != "extra" {
		t.Errorf("install row repo %v", r["repo"])
	}
	_, evs = runPkg(t, p, "plan-remove", "foo-old")
	check("remove", evs)
	// a stale root run emits the plan before its result
	h := confirmedHash(t, p, "plan-install", "foo")
	p.planItems = append(p.planItems, "extra surprise 9-1")
	_, evs = runPkg(t, p, "pkg-install", h, "foo")
	check("stale", evs)
	if evs[len(evs)-1]["t"] != EventResult || evs[len(evs)-2]["t"] != EventPlan {
		t.Error("the stale plan does not come right before the result")
	}
}
