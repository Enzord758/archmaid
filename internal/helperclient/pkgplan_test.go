package helperclient

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/steps"
)

// The Packages verbs' client side (pkgplan.go), against the contract in
// docs/design/packages-contract.md section 3: the plan hash, the plan
// check, the root argv, the plan call with a fake helper script, and the
// refusals. Neither the real helper, pkexec nor pacman is reached.

func withHash(p steps.PkgPlan) steps.PkgPlan {
	p.Hash = PkgPlanHash(p)
	return p
}

func installPkgPlan() steps.PkgPlan {
	return withHash(steps.PkgPlan{Mode: steps.PkgInstall, Targets: []string{"foo", "bar"},
		Rows: []steps.PkgRow{
			{Name: "foo", Version: "1.2-1", Repo: "extra", Target: true, Download: 100, Installed: 300},
			{Name: "bar", Version: "2:3-1", Repo: "cachyos-v3", Target: true},
			{Name: "libz", Version: "1-1", Repo: "core"},
		}})
}

func removePkgPlan(config bool) steps.PkgPlan {
	return withHash(steps.PkgPlan{Mode: steps.PkgRemove, WithConfig: config, Targets: []string{"foo"},
		Rows: []steps.PkgRow{{Name: "foo", Version: "1.2-1", Target: true}, {Name: "libz", Version: "1-1"}}})
}

// TestPkgPlanHashPinned fixes the hash format, which the helper must
// reproduce byte for byte (Dev 3's PkgPlanHash is tied to this by a test
// once both are merged).
func TestPkgPlanHashPinned(t *testing.T) {
	p := steps.PkgPlan{Mode: steps.PkgInstall, AsDeps: true, Targets: []string{"b", "a"},
		Rows: []steps.PkgRow{{Name: "b", Version: "1-1", Repo: "extra"}, {Name: "a", Version: "2-1", Repo: "core"}}}
	got := PkgPlanHash(p)
	if !hashPattern.MatchString(got) {
		t.Fatalf("hash %q", got)
	}
	// Order independence of targets and rows.
	q := p
	q.Targets, q.Rows = []string{"a", "b"}, []steps.PkgRow{p.Rows[1], p.Rows[0]}
	if PkgPlanHash(q) != got {
		t.Error("the hash depends on the order of targets or rows")
	}
	// Everything the hash commits to changes it; what it doesn't commit to doesn't.
	changes := map[string]func(*steps.PkgPlan){
		"mode":    func(p *steps.PkgPlan) { p.Mode = steps.PkgRemove },
		"asdeps":  func(p *steps.PkgPlan) { p.AsDeps = false },
		"target":  func(p *steps.PkgPlan) { p.Targets = []string{"a", "c"} },
		"version": func(p *steps.PkgPlan) { p.Rows = []steps.PkgRow{p.Rows[0], {Name: "a", Version: "2-2", Repo: "core"}} },
		"repo":    func(p *steps.PkgPlan) { p.Rows = []steps.PkgRow{p.Rows[0], {Name: "a", Version: "2-1", Repo: "extra"}} },
		"extra": func(p *steps.PkgPlan) {
			p.Rows = append(slices.Clone(p.Rows), steps.PkgRow{Name: "c", Version: "1", Repo: "core"})
		},
	}
	for name, mod := range changes {
		c := p
		c.Rows, c.Targets = slices.Clone(p.Rows), slices.Clone(p.Targets)
		mod(&c)
		if PkgPlanHash(c) == got {
			t.Errorf("changing the %s does not change the hash", name)
		}
	}
	same := p
	same.Rows = slices.Clone(p.Rows)
	same.Rows[0].Download, same.Rows[0].Installed, same.Rows[0].Target, same.Rows[0].OldVersion = 9, 9, true, "0-1"
	same.Warnings = []string{"w"}
	if PkgPlanHash(same) != got {
		t.Error("sizes, flags and warnings are in the hash")
	}
}

func TestCheckPkgPlan(t *testing.T) {
	if err := CheckPkgPlan(installPkgPlan()); err != nil {
		t.Fatalf("good install plan: %v", err)
	}
	if err := CheckPkgPlan(removePkgPlan(true)); err != nil {
		t.Fatalf("good remove plan: %v", err)
	}
	bad := map[string]func(*steps.PkgPlan){
		"hash shape":     func(p *steps.PkgPlan) { p.Hash = "sha256:abc" },
		"hash of others": func(p *steps.PkgPlan) { p.Rows[2].Version = "9-9" },
		"no targets":     func(p *steps.PkgPlan) { p.Targets, p.Rows = nil, nil },
		"option name":    func(p *steps.PkgPlan) { p.Targets[0] = "-Rdd" },
		"repo/pkg":       func(p *steps.PkgPlan) { p.Targets[0] = "extra/foo" },
		"target twice":   func(p *steps.PkgPlan) { p.Targets[1] = "foo" },
		"row twice":      func(p *steps.PkgPlan) { p.Rows[2].Name = "foo" },
		"target no row":  func(p *steps.PkgPlan) { p.Rows = p.Rows[1:] },
		"flag mismatch":  func(p *steps.PkgPlan) { p.Rows[2].Target = true },
		"bad version":    func(p *steps.PkgPlan) { p.Rows[0].Version = "1 2" },
		"empty repo":     func(p *steps.PkgPlan) { p.Rows[0].Repo = "" },
		"bad repo":       func(p *steps.PkgPlan) { p.Rows[0].Repo = "ex/tra" },
		"update row":     func(p *steps.PkgPlan) { p.Rows[0].OldVersion = "1.1-1" },
		"negative size":  func(p *steps.PkgPlan) { p.Rows[0].Download = -1 },
		"config install": func(p *steps.PkgPlan) { p.WithConfig = true },
		"unknown mode":   func(p *steps.PkgPlan) { p.Mode = 7 },
		"33 targets":     func(p *steps.PkgPlan) { p.Targets = make([]string, 33) },
	}
	for name, mod := range bad {
		t.Run(name, func(t *testing.T) {
			p := installPkgPlan()
			p.Targets, p.Rows = slices.Clone(p.Targets), slices.Clone(p.Rows)
			mod(&p)
			if name != "hash shape" && name != "hash of others" {
				if p.Mode == steps.PkgInstall || name == "unknown mode" {
					p.Hash = PkgPlanHash(p) // otherwise the hash would be the reason
				}
			}
			if err := CheckPkgPlan(p); !errors.Is(err, ErrBadPlan) {
				t.Errorf("err = %v, want ErrBadPlan", err)
			}
		})
	}
	// A remove row with a repo, a remove with --asdeps.
	r := removePkgPlan(false)
	r.Rows = slices.Clone(r.Rows)
	r.Rows[1].Repo = "core"
	r.Hash = PkgPlanHash(r)
	if err := CheckPkgPlan(r); !errors.Is(err, ErrBadPlan) {
		t.Errorf("remove row with a repo: %v", err)
	}
	r = removePkgPlan(false)
	r.AsDeps = true
	r.Hash = PkgPlanHash(r)
	if err := CheckPkgPlan(r); !errors.Is(err, ErrBadPlan) {
		t.Errorf("remove --asdeps: %v", err)
	}
}

func pkgStep(verb string, p steps.PkgPlan) steps.Step {
	return steps.Step{ID: "x", Verb: verb, Items: slices.Clone(p.Targets), PkgPlan: &p}
}

// TestPkgArgv: the root argv is the hash, the fixed option token, the
// names; nothing of the plan's rows reaches it.
func TestPkgArgv(t *testing.T) {
	in := installPkgPlan()
	asdeps := in
	asdeps.AsDeps = true
	asdeps.Hash = PkgPlanHash(asdeps)
	for _, tc := range []struct {
		name string
		st   steps.Step
		want []string
	}{
		{"install", pkgStep(steps.VerbPkgInstall, in), []string{in.Hash, "foo", "bar"}},
		{"install asdeps", pkgStep(steps.VerbPkgInstall, asdeps), []string{asdeps.Hash, "--asdeps", "foo", "bar"}},
		{"remove", pkgStep(steps.VerbPkgRemove, removePkgPlan(false)), []string{removePkgPlan(false).Hash, "foo"}},
		{"remove -n", pkgStep(steps.VerbPkgRemove, removePkgPlan(true)), []string{removePkgPlan(true).Hash, "-n", "foo"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Argv(tc.st)
			if err != nil {
				t.Fatal(err)
			}
			want := append([]string{Pkexec, DisableInternalAgent, HelperPath, tc.st.Verb}, tc.want...)
			if !slices.Equal(got, want) {
				t.Errorf("argv = %q\nwant   %q", got, want)
			}
		})
	}
}

func TestPkgArgvRefuses(t *testing.T) {
	in := installPkgPlan()
	tampered := in
	tampered.Rows = slices.Clone(in.Rows)
	tampered.Rows[2].Version = "666-1"
	otherItems := pkgStep(steps.VerbPkgInstall, in)
	otherItems.Items = []string{"foo", "evil"}
	for name, tc := range map[string]struct {
		st   steps.Step
		want error
	}{
		"no plan":          {steps.Step{ID: "x", Verb: steps.VerbPkgInstall, Items: []string{"foo"}}, ErrUnsupported},
		"tampered plan":    {pkgStep(steps.VerbPkgInstall, tampered), ErrBadPlan},
		"items not target": {otherItems, ErrBadPlan},
		"mode vs verb":     {pkgStep(steps.VerbPkgRemove, in), ErrBadPlan},
		"plan on others":   {pkgStep(steps.VerbRepair, in), ErrBadPlan},
		"plan verb":        {pkgStep(steps.VerbPlanInstall, in), ErrBadPlan},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Argv(tc.st); !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
		})
	}
	if _, err := Argv(steps.Step{ID: "x", Verb: steps.VerbPlanRemove}); !errors.Is(err, ErrNoAction) {
		t.Errorf("a plan verb as a step: %v", err)
	}
}

// planLineFor is the helper's plan event for p, as the contract spells it.
func planLineFor(t *testing.T, p steps.PkgPlan, mut func(map[string]any)) string {
	t.Helper()
	mode := "install"
	if p.Mode == steps.PkgRemove {
		mode = "remove"
	}
	var items []string
	var rows []map[string]any
	for _, r := range p.Rows {
		items = append(items, r.Name+" "+r.Version)
		rows = append(rows, map[string]any{"name": r.Name, "version": r.Version, "old": r.OldVersion, "repo": r.Repo,
			"target": r.Target, "dl": r.Download, "inst": r.Installed})
	}
	m := map[string]any{"v": 1, "t": "plan", "mode": mode, "with_config": p.WithConfig, "asdeps": p.AsDeps,
		"targets": p.Targets, "items": items, "total": len(items), "hash": p.Hash, "rows": rows}
	if mut != nil {
		mut(m)
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

const (
	startPlanInstall = `{"v":1,"t":"start","verb":"plan-install","version":"0.1.0"}`
	startPlanRemove  = `{"v":1,"t":"start","verb":"plan-remove","version":"0.1.0"}`
	startPkgInstall  = `{"v":1,"t":"start","verb":"pkg-install","version":"0.1.0"}`
)

func resultLine(code int, extra string) string {
	ok := "false"
	if code == 0 {
		ok = "true"
	}
	if extra != "" {
		extra = "," + extra
	}
	return `{"v":1,"t":"result","ok":` + ok + `,"code":` + itoa(code) + `,"child_exit":null,"skipped":[]` + extra + `}`
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func planPkgs(t *testing.T, helper string, req steps.PkgRequest) (steps.PkgPlan, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return planPackages(ctx, helper, req)
}

func TestPlanPackagesInstall(t *testing.T) {
	p := installPkgPlan()
	helper, argv := fakeHelper(t, "cat <<'EOF'\n"+startPlanInstall+"\n"+planLineFor(t, p, nil)+"\n"+resultLine(0, "")+"\nEOF")
	got, err := planPkgs(t, helper, steps.PkgRequest{Mode: steps.PkgInstall, Names: []string{"foo", "bar"}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Hash != p.Hash || len(got.Rows) != 3 || got.Rows[1].Repo != "cachyos-v3" || got.Rows[0].Download != 100 {
		t.Errorf("plan = %+v", got)
	}
	if a := readArgv(t, argv); !slices.Equal(a, []string{"plan-install", "foo", "bar"}) {
		t.Errorf("helper argv = %q", a)
	}
}

func TestPlanPackagesRemoveWithConfig(t *testing.T) {
	p := removePkgPlan(true)
	helper, argv := fakeHelper(t, "cat <<'EOF'\n"+startPlanRemove+"\n"+planLineFor(t, p, nil)+"\n"+resultLine(0, "")+"\nEOF")
	got, err := planPkgs(t, helper, steps.PkgRequest{Mode: steps.PkgRemove, Names: []string{"foo"}, WithConfig: true})
	if err != nil || !got.WithConfig {
		t.Fatalf("plan = %+v, err = %v", got, err)
	}
	if a := readArgv(t, argv); !slices.Equal(a, []string{"plan-remove", "-n", "foo"}) {
		t.Errorf("helper argv = %q", a)
	}
}

// TestPlanPackagesRefusals: every refusal reason the contract names is a
// *PkgRefusal with its names and count, for the exit code the helper uses.
func TestPlanPackagesRefusals(t *testing.T) {
	for _, tc := range []struct {
		code   int
		reason steps.RefuseReason
		extra  string
		names  []string
		count  int
	}{
		{3, steps.RefuseUpdatesPending, `"reason":"updates-pending","count":7,"error":"7 updates are waiting"`, nil, 7},
		{4, steps.RefuseConflict, `"reason":"conflict","names":["vim"],"error":"gvim replaces vim"`, []string{"vim"}, 0},
		{4, steps.RefuseProtected, `"reason":"protected","names":["glibc","bad name"],"error":"protected"`, []string{"glibc"}, 0},
		{2, steps.RefuseNotFound, `"reason":"not-found","names":["nope"]`, []string{"nope"}, 0},
		{4, steps.RefuseRequired, `"reason":"required-by","names":["libz"]`, []string{"libz"}, 0},
		{5, steps.RefuseBusy, `"reason":"busy","error":"pacman is running"`, nil, 0},
	} {
		t.Run(string(tc.reason), func(t *testing.T) {
			helper, _ := fakeHelper(t, "cat <<'EOF'\n"+startPlanInstall+"\n"+resultLine(tc.code, tc.extra)+"\nEOF\nexit "+itoa(tc.code))
			_, err := planPkgs(t, helper, steps.PkgRequest{Mode: steps.PkgInstall, Names: []string{"foo"}})
			var pr *steps.PkgRefusal
			if !errors.As(err, &pr) {
				t.Fatalf("err = %v, want a *PkgRefusal", err)
			}
			if pr.Reason != tc.reason || !slices.Equal(pr.Names, tc.names) || pr.Count != tc.count || pr.Detail == "" {
				t.Errorf("refusal = %+v", pr)
			}
		})
	}
}

// TestPlanPackagesFailsClosed: anything but exactly one valid plan for the
// request, with the helper's success, is an error and no plan.
func TestPlanPackagesFailsClosed(t *testing.T) {
	good := installPkgPlan()
	req := steps.PkgRequest{Mode: steps.PkgInstall, Names: []string{"foo", "bar"}}
	otherReq := withHash(steps.PkgPlan{Mode: steps.PkgInstall, Targets: []string{"foo"},
		Rows: []steps.PkgRow{{Name: "foo", Version: "1-1", Repo: "extra", Target: true}}})
	for name, tc := range map[string]struct{ body string }{
		"no plan event":   {startPlanInstall + "\n" + resultLine(0, "")},
		"two plans":       {startPlanInstall + "\n" + planLineFor(t, good, nil) + "\n" + planLineFor(t, good, nil) + "\n" + resultLine(0, "")},
		"rows missing":    {startPlanInstall + "\n" + planLineFor(t, good, func(m map[string]any) { delete(m, "rows") }) + "\n" + resultLine(0, "")},
		"total disagree":  {startPlanInstall + "\n" + planLineFor(t, good, func(m map[string]any) { m["total"] = 9 }) + "\n" + resultLine(0, "")},
		"bad hash":        {startPlanInstall + "\n" + planLineFor(t, good, func(m map[string]any) { m["hash"] = "sha256:" + strings.Repeat("0", 64) }) + "\n" + resultLine(0, "")},
		"item vs row":     {startPlanInstall + "\n" + planLineFor(t, good, func(m map[string]any) { m["items"] = []string{"x 1", "y 1", "z 1"} }) + "\n" + resultLine(0, "")},
		"unknown mode":    {startPlanInstall + "\n" + planLineFor(t, good, func(m map[string]any) { m["mode"] = "upgrade" }) + "\n" + resultLine(0, "")},
		"plan for others": {startPlanInstall + "\n" + planLineFor(t, otherReq, nil) + "\n" + resultLine(0, "")},
		"wrong verb":      {startPlanRemove + "\n" + planLineFor(t, good, nil) + "\n" + resultLine(0, "")},
		"failed":          {startPlanInstall + "\n" + resultLine(1, `"error":"boom"`) + "\nexit 1"},
	} {
		t.Run(name, func(t *testing.T) {
			helper, _ := fakeHelper(t, "cat <<'EOF'\n"+tc.body+"\nEOF")
			p, err := planPkgs(t, helper, req)
			var pr *steps.PkgRefusal
			if err == nil || errors.As(err, &pr) || len(p.Rows) != 0 {
				t.Errorf("plan %+v, err %v: want an error and no plan", p, err)
			}
		})
	}
}

// TestPlanPackagesRequestChecked: a bad request never starts the helper.
func TestPlanPackagesRequestChecked(t *testing.T) {
	helper, argv := fakeHelper(t, "exit 0")
	many := make([]string, 33)
	for i := range many {
		many[i] = "p" + itoa(i)
	}
	for name, req := range map[string]steps.PkgRequest{
		"no names":          {Mode: steps.PkgInstall},
		"33 names":          {Mode: steps.PkgInstall, Names: many},
		"option name":       {Mode: steps.PkgInstall, Names: []string{"--overwrite"}},
		"repo/name":         {Mode: steps.PkgInstall, Names: []string{"extra/foo"}},
		"duplicate":         {Mode: steps.PkgInstall, Names: []string{"a", "a"}},
		"config on install": {Mode: steps.PkgInstall, Names: []string{"a"}, WithConfig: true},
		"asdeps on remove":  {Mode: steps.PkgRemove, Names: []string{"a"}, AsDeps: true},
		"bad mode":          {Mode: 5, Names: []string{"a"}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := planPkgs(t, helper, req)
			var pr *steps.PkgRefusal
			if !errors.As(err, &pr) || pr.Reason != steps.RefuseBadRequest {
				t.Errorf("err = %v, want bad-request", err)
			}
		})
	}
	if _, err := readArgvMaybe(argv); err == nil {
		t.Error("the helper was started for a bad request")
	}
}

// decodeRun feeds lines to a decoder for st and returns the outcome of an
// exit with code, as Run would see a pkexec'd helper.
func decodeRun(st steps.Step, code int, lines ...string) Outcome {
	d := newDecoder(st)
	for _, l := range lines {
		d.feed([]byte(l))
	}
	return d.outcome(code, nil, "")
}

// TestRootVerbStaleCarriesTheNewPlan: pkg-install finding the confirmed
// plan stale (exit 3, nothing run, the new plan emitted) is a
// RefuseStale refusal with that plan, so the page can review again.
func TestRootVerbStaleCarriesTheNewPlan(t *testing.T) {
	old := installPkgPlan()
	newer := withHash(steps.PkgPlan{Mode: steps.PkgInstall, Targets: old.Targets,
		Rows: append(slices.Clone(old.Rows), steps.PkgRow{Name: "libnew", Version: "1-1", Repo: "extra"})})
	st := pkgStep(steps.VerbPkgInstall, old)
	for name, result := range map[string]string{
		"with reason":   resultLine(3, `"reason":"stale","error":"the plan changed"`),
		"by exit alone": resultLine(3, `"error":"the plan changed"`),
	} {
		t.Run(name, func(t *testing.T) {
			o := decodeRun(st, 3, startPkgInstall, planLineFor(t, newer, nil), result)
			var pr *steps.PkgRefusal
			if !errors.As(o.Err, &pr) || pr.Reason != steps.RefuseStale || pr.Plan == nil || len(pr.Plan.Rows) != 4 {
				t.Fatalf("err = %v (%+v), want stale with the new plan", o.Err, pr)
			}
		})
	}
	// An unrelated exit 3 with a command already run is an ordinary failure.
	o := decodeRun(st, 3, startPkgInstall, `{"v":1,"t":"result","ok":false,"code":3,"child_exit":1,"skipped":[]}`)
	var pr *steps.PkgRefusal
	if o.Err == nil || errors.As(o.Err, &pr) {
		t.Errorf("err = %v, want a plain failure", o.Err)
	}
}

func TestRootVerbRefusals(t *testing.T) {
	st := pkgStep(steps.VerbPkgInstall, installPkgPlan())
	for _, tc := range []struct {
		code   int
		extra  string
		reason steps.RefuseReason
	}{
		{3, `"reason":"updates-pending","count":3`, steps.RefuseUpdatesPending},
		{4, `"reason":"protected","names":["glibc"]`, steps.RefuseProtected},
		{4, `"reason":"conflict","names":["vim"]`, steps.RefuseConflict},
		{5, `"reason":"busy"`, steps.RefuseBusy},
	} {
		t.Run(string(tc.reason), func(t *testing.T) {
			o := decodeRun(st, tc.code, startPkgInstall, resultLine(tc.code, tc.extra))
			var pr *steps.PkgRefusal
			if !errors.As(o.Err, &pr) || pr.Reason != tc.reason {
				t.Errorf("err = %v", o.Err)
			}
		})
	}
	// A reason on a verb that is not a Packages verb is not a refusal.
	o := decodeRun(steps.Step{ID: "r", Verb: steps.VerbRepair, Items: []string{"a"}}, 4,
		`{"v":1,"t":"start","verb":"repair","version":"0.1.0"}`, resultLine(4, `"reason":"protected"`))
	var pr *steps.PkgRefusal
	if o.Err == nil || errors.As(o.Err, &pr) {
		t.Errorf("repair: err = %v", o.Err)
	}
	// A pkexec refusal (127, no output) is still not-granted for a root verb.
	if o := decodeRun(st, 127); !o.NotGranted {
		t.Errorf("exit 127 = %+v, want NotGranted", o)
	}
}

// readArgvMaybe is the argv file fakeHelper's script writes when it runs.
func readArgvMaybe(file string) ([]byte, error) { return os.ReadFile(file) }

// TestPkgPlanHashIsTheHelpersText pins the exact text hashed (the helper's
// PkgPlanHash, Dev 3), independently of the code under test.
func TestPkgPlanHashIsTheHelpersText(t *testing.T) {
	sum := func(s string) string { h := sha256.Sum256([]byte(s)); return "sha256:" + hex.EncodeToString(h[:]) }
	in := steps.PkgPlan{Mode: steps.PkgInstall, Targets: []string{"b", "a"},
		Rows: []steps.PkgRow{{Name: "b", Version: "1-1", Repo: "extra"}, {Name: "a", Version: "2-1", Repo: "core"}}}
	if got, want := PkgPlanHash(in), sum("archmaid pkg plan v1\nmode install\ntarget a\ntarget b\nitem core a 2-1\nitem extra b 1-1\n"); got != want {
		t.Errorf("install: %s, want %s", got, want)
	}
	in.AsDeps = true
	if got, want := PkgPlanHash(in), sum("archmaid pkg plan v1\nmode install-asdeps\ntarget a\ntarget b\nitem core a 2-1\nitem extra b 1-1\n"); got != want {
		t.Errorf("asdeps: %s, want %s", got, want)
	}
	rm := steps.PkgPlan{Mode: steps.PkgRemove, Targets: []string{"a"}, Rows: []steps.PkgRow{{Name: "z", Version: "1"}, {Name: "a", Version: "2-1"}}}
	if got, want := PkgPlanHash(rm), sum("archmaid pkg plan v1\nmode remove\ntarget a\nitem a 2-1\nitem z 1\n"); got != want {
		t.Errorf("remove: %s, want %s", got, want)
	}
	rm.WithConfig = true
	if got, want := PkgPlanHash(rm), sum("archmaid pkg plan v1\nmode remove-n\ntarget a\nitem a 2-1\nitem z 1\n"); got != want {
		t.Errorf("remove -n: %s, want %s", got, want)
	}
}
