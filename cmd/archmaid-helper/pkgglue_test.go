package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/cmd/archmaid-helper/internal/helper"
	"github.com/Enzord758/archmaid/internal/helperclient"
	"github.com/Enzord758/archmaid/internal/steps"
)

// The Packages glue (docs/design/packages-contract.md): the helper's
// verbs and the client's parser and hash are separate package trees, so the
// ties between them live here, in the one test package that may import both
// (cmd/archmaid-helper may see its own internal/helper).

// pkgFake is a fake pacman for the package verbs: the helper's own
// pkgSys fixtures (internal/helper/pkg_test.go), trimmed. It fails the test
// on any -S or -R that is not a read (--print / -Sp), and records the root
// commands without running them.
type pkgFake struct {
	t *testing.T

	sync      map[string]bool
	installed map[string]string
	updates   []string
	planItems []string // -Sp: "repo name version"
	rel       map[string]string
	rmItems   []string // -Rs --print: "name version"
	rmErr     string

	streams [][]string
}

func newPkgFake(t *testing.T) *pkgFake {
	return &pkgFake{
		t:         t,
		sync:      map[string]bool{"foo": true, "bar": true, "libfoo": true},
		installed: map[string]string{"bash": "5.2-1", "glibc": "2.40-1", "libold": "1.0-1", "foo-old": "1-1", "base": "3-1"},
		planItems: []string{"extra foo 1.0-1", "cachyos-v3 libfoo 2:2.0-1"},
		rel:       map[string]string{},
		rmItems:   []string{"foo-old 1-1", "libold 1.0-1"},
	}
}

func (p *pkgFake) Query(argv []string) (string, string, int, error) {
	if len(argv) < 2 || argv[0] != helper.Pacman {
		p.t.Fatalf("query is not pacman: %q", argv)
	}
	args := argv[2:]
	var names []string
	if i := slices.Index(args, "--"); i >= 0 {
		names = args[i+1:]
	}
	switch argv[1] {
	case "-Si":
		if len(names) == 1 && !strings.Contains(names[0], "/") {
			if p.sync[names[0]] {
				return "Name : " + names[0] + "\n", "", 0, nil
			}
			return "", "error: package '" + names[0] + "' was not found", 1, nil
		}
		var b strings.Builder
		for _, spec := range names {
			repo, n, _ := strings.Cut(spec, "/")
			fmt.Fprintf(&b, "Repository      : %s\nName            : %s\nVersion         : 1\nDownload Size   : 1.50 MiB\nInstalled Size  : 4.00 KiB\n%s\nDescription     : x\n\n", repo, n, p.rel[n])
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
		return "linux-cachyos\n", "", 0, nil
	case "-Qi":
		if len(names) == 1 && names[0] == "base" {
			return "Name : base\nDepends On      : filesystem glibc>=2.40\n", "", 0, nil
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
			return "", p.rmErr, 1, nil
		}
		return strings.Join(p.rmItems, "\n") + "\n", "", 0, nil
	}
	p.t.Fatalf("unexpected query: %q", argv)
	return "", "", 2, nil
}

func (p *pkgFake) Stream(argv []string, line func(stream, text string)) (int, error) {
	p.streams = append(p.streams, slices.Clone(argv))
	if len(argv) < 2 || argv[0] != helper.Pacman || !slices.Contains([]string{"-S", "-Rs", "-Rns"}, argv[1]) {
		p.t.Fatalf("unexpected root command: %q", argv)
	}
	line("out", "done")
	return 0, nil
}

// run runs the helper in-process (a dev build: no root, the lock folder is
// the test's) and returns its exit code and raw JSON-lines output.
func (p *pkgFake) run(args ...string) (int, string) {
	p.t.Helper()
	var out bytes.Buffer
	code := helper.Run(args, p.opts(&out, p.t.TempDir()))
	return code, out.String()
}

func (p *pkgFake) opts(out *bytes.Buffer, dir string) helper.Options {
	return helper.Options{
		Runner: p, Stdout: out, Euid: 1000, Dev: true,
		LockPath: filepath.Join(dir, "lock"), PacmanDBLock: filepath.Join(dir, "db.lck"),
		KernelPkgbases: func() ([]string, error) { return []string{"/usr/lib/modules/6.1.0-1/pkgbase"}, nil },
	}
}

// parse reads the helper output the way PlanPackages does.
func parse(verb string, req steps.PkgRequest, out string, exit int) (steps.PkgPlan, error) {
	return helperclient.ParsePkgPlan(verb, req, strings.NewReader(out), exit)
}

// TestPkgHashClientIsTheHelpers: helper.PkgPlanHash and
// helperclient.PkgPlanHash give the same hash for the same plan in all four
// modes, and both give the hash of the documented text (a golden table, so
// a change on one side alone fails here). The test lives in package main of
// cmd/archmaid-helper because that is the one place that may import both.
func TestPkgHashClientIsTheHelpers(t *testing.T) {
	sum := func(text string) string {
		d := sha256.Sum256([]byte(text))
		return "sha256:" + hex.EncodeToString(d[:])
	}
	inst := []steps.PkgRow{{Name: "foo", Version: "1.0-1", Repo: "extra", Target: true}, {Name: "libfoo", Version: "2:2.0-1", Repo: "cachyos-v3"}}
	rm := []steps.PkgRow{{Name: "foo-old", Version: "1-1", Target: true}, {Name: "libold", Version: "1.0-1"}}
	for _, c := range []struct {
		mode  string
		plan  steps.PkgPlan
		items []string // the helper's hash items for this mode, in a different order than sorted
		text  string
	}{
		{"install", steps.PkgPlan{Mode: steps.PkgInstall, Targets: []string{"foo"}, Rows: inst},
			[]string{"extra foo 1.0-1", "cachyos-v3 libfoo 2:2.0-1"},
			"archmaid pkg plan v1\nmode install\ntarget foo\nitem cachyos-v3 libfoo 2:2.0-1\nitem extra foo 1.0-1\n"},
		{"install-asdeps", steps.PkgPlan{Mode: steps.PkgInstall, AsDeps: true, Targets: []string{"foo"}, Rows: inst},
			[]string{"extra foo 1.0-1", "cachyos-v3 libfoo 2:2.0-1"},
			"archmaid pkg plan v1\nmode install-asdeps\ntarget foo\nitem cachyos-v3 libfoo 2:2.0-1\nitem extra foo 1.0-1\n"},
		{"remove", steps.PkgPlan{Mode: steps.PkgRemove, Targets: []string{"foo-old"}, Rows: rm},
			[]string{"libold 1.0-1", "foo-old 1-1"},
			"archmaid pkg plan v1\nmode remove\ntarget foo-old\nitem foo-old 1-1\nitem libold 1.0-1\n"},
		{"remove-n", steps.PkgPlan{Mode: steps.PkgRemove, WithConfig: true, Targets: []string{"foo-old"}, Rows: rm},
			[]string{"foo-old 1-1", "libold 1.0-1"},
			"archmaid pkg plan v1\nmode remove-n\ntarget foo-old\nitem foo-old 1-1\nitem libold 1.0-1\n"},
	} {
		t.Run(c.mode, func(t *testing.T) {
			client, hlp := helperclient.PkgPlanHash(c.plan), helper.PkgPlanHash(c.mode, c.plan.Targets, c.items)
			if client != hlp {
				t.Errorf("client %s, helper %s", client, hlp)
			}
			if want := sum(c.text); client != want || hlp != want {
				t.Errorf("client %s, helper %s, golden %s", client, hlp, want)
			}
		})
	}
}

// pkgRequests: the four requests a front end can make.
var pkgRequests = []struct {
	name string
	req  steps.PkgRequest
	args []string // helper args after the plan verb
}{
	{"install", steps.PkgRequest{Mode: steps.PkgInstall, Names: []string{"foo"}}, []string{"foo"}},
	{"install-asdeps", steps.PkgRequest{Mode: steps.PkgInstall, AsDeps: true, Names: []string{"foo"}}, []string{"--asdeps", "foo"}},
	{"remove", steps.PkgRequest{Mode: steps.PkgRemove, Names: []string{"foo-old"}}, []string{"foo-old"}},
	{"remove-n", steps.PkgRequest{Mode: steps.PkgRemove, WithConfig: true, Names: []string{"foo-old"}}, []string{"-n", "foo-old"}},
}

func planVerb(r steps.PkgRequest) string {
	if r.Mode == steps.PkgRemove {
		return steps.VerbPlanRemove
	}
	return steps.VerbPlanInstall
}

func runVerb(r steps.PkgRequest) string {
	if r.Mode == steps.PkgRemove {
		return steps.VerbPkgRemove
	}
	return steps.VerbPkgInstall
}

// TestHelperPlanThroughClientParser: the helper's real plan-install /
// plan-remove output, run against the pacman fake, is read by the client's
// parser into a plan that is checked (CheckPkgPlan), carries the helper's
// hash, and whose root argv is one the helper's pkg-install / pkg-remove
// accepts on the same system, running exactly the confirmed names.
func TestHelperPlanThroughClientParser(t *testing.T) {
	for _, c := range pkgRequests {
		t.Run(c.name, func(t *testing.T) {
			p := newPkgFake(t)
			code, out := p.run(append([]string{planVerb(c.req)}, c.args...)...)
			if code != helper.ExitOK {
				t.Fatalf("plan exited %d: %s", code, out)
			}
			plan, err := parse(planVerb(c.req), c.req, out, code)
			if err != nil {
				t.Fatalf("ParsePkgPlan: %v\n%s", err, out)
			}
			if plan.Mode != c.req.Mode || plan.AsDeps != c.req.AsDeps || plan.WithConfig != c.req.WithConfig {
				t.Errorf("plan flags %+v for request %+v", plan, c.req)
			}
			if len(plan.Rows) != 2 || plan.Hash != helperclient.PkgPlanHash(plan) {
				t.Fatalf("rows %d, hash %s vs client %s", len(plan.Rows), plan.Hash, helperclient.PkgPlanHash(plan))
			}
			for _, r := range plan.Rows {
				if r.Target != slices.Contains(c.req.Names, r.Name) || r.Version == "" {
					t.Errorf("row %+v", r)
				}
				if (c.req.Mode == steps.PkgInstall) != (r.Repo != "") {
					t.Errorf("row %+v: repo set wrongly for the mode", r)
				}
			}
			if c.req.Mode == steps.PkgInstall && (plan.Rows[0].Download == 0 || plan.Rows[0].Installed == 0) {
				t.Errorf("sizes were lost: %+v", plan.Rows[0])
			}
			// Round trip: the client's argv for the confirmed plan, run by the helper.
			argv, err := helperclient.Argv(steps.Step{ID: "x", Verb: runVerb(c.req), Items: plan.Targets, PkgPlan: &plan})
			if err != nil {
				t.Fatal(err)
			}
			code, out = p.run(helperArgs(t, argv)...)
			if code != helper.ExitOK || len(p.streams) != 1 {
				t.Fatalf("root verb exited %d, ran %v: %s", code, p.streams, out)
			}
			for _, n := range c.req.Names {
				if !slices.Contains(p.streams[0], n) {
					t.Errorf("%s was not in %q", n, p.streams[0])
				}
			}
		})
	}
}

// TestHelperRefusalsThroughClientParser: every refusal the helper reports
// reaches the client as a *PkgRefusal with the same reason, names and count,
// and no plan.
func TestHelperRefusalsThroughClientParser(t *testing.T) {
	inst := steps.PkgRequest{Mode: steps.PkgInstall, Names: []string{"foo"}}
	rm := steps.PkgRequest{Mode: steps.PkgRemove, Names: []string{"foo-old"}}
	for _, c := range []struct {
		name   string
		setup  func(*pkgFake)
		req    steps.PkgRequest
		args   []string
		reason steps.RefuseReason
		names  []string
		count  int
	}{
		{"not-found", func(*pkgFake) {}, steps.PkgRequest{Mode: steps.PkgInstall, Names: []string{"nonesuch"}}, []string{"nonesuch"}, steps.RefuseNotFound, []string{"nonesuch"}, 0},
		{"updates-pending", func(p *pkgFake) { p.updates = []string{"glibc 2.40-1 -> 2.41-1", "bash 5.2-1 -> 5.3-1"} }, inst, []string{"foo"}, steps.RefuseUpdatesPending, nil, 2},
		{"upgrade", func(p *pkgFake) { p.installed["libfoo"] = "1.0-1" }, inst, []string{"foo"}, steps.RefuseUpdatesPending, []string{"libfoo"}, 0},
		{"conflict", func(p *pkgFake) { p.rel["foo"] = "Conflicts With  : foo-old" }, inst, []string{"foo"}, steps.RefuseConflict, []string{"foo", "foo-old"}, 0},
		{"not-installed", func(*pkgFake) {}, steps.PkgRequest{Mode: steps.PkgRemove, Names: []string{"ghost"}}, []string{"ghost"}, steps.RefuseNotInstalled, []string{"ghost"}, 0},
		{"protected", func(*pkgFake) {}, steps.PkgRequest{Mode: steps.PkgRemove, Names: []string{"bash"}}, []string{"bash"}, steps.RefuseProtected, []string{"bash"}, 0},
		{"required-by", func(p *pkgFake) { p.rmErr = "error: failed to prepare transaction (could not satisfy dependencies)" }, rm, []string{"foo-old"}, steps.RefuseRequired, nil, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := newPkgFake(t)
			c.setup(p)
			code, out := p.run(append([]string{planVerb(c.req)}, c.args...)...)
			if code == helper.ExitOK {
				t.Fatalf("exit 0, want a refusal: %s", out)
			}
			_, err := parse(planVerb(c.req), c.req, out, code)
			var ref *steps.PkgRefusal
			if !errors.As(err, &ref) {
				t.Fatalf("err = %v, want *PkgRefusal\n%s", err, out)
			}
			if ref.Reason != c.reason || !slices.Equal(ref.Names, c.names) || ref.Count != c.count || ref.Plan != nil || ref.Detail == "" {
				t.Errorf("refusal %+v, want %s %q %d", ref, c.reason, c.names, c.count)
			}
			if len(p.streams) != 0 {
				t.Errorf("a refusal ran %v", p.streams)
			}
		})
	}
	t.Run("bad-request", func(t *testing.T) {
		p := newPkgFake(t)
		code, out := p.run(steps.VerbPlanInstall, "-x")
		if code != helper.ExitUsage {
			t.Fatalf("exit %d", code)
		}
		var ref *steps.PkgRefusal
		if _, err := parse(steps.VerbPlanInstall, inst, out, code); !errors.As(err, &ref) || ref.Reason != steps.RefuseBadRequest {
			t.Errorf("err = %v, want bad-request", err)
		}
	})
	t.Run("busy", func(t *testing.T) {
		p := newPkgFake(t)
		code, out := p.run(steps.VerbPlanInstall, "foo")
		plan, err := parse(steps.VerbPlanInstall, inst, out, code)
		if code != helper.ExitOK || err != nil {
			t.Fatal(code, err)
		}
		var buf bytes.Buffer
		o := p.opts(&buf, t.TempDir())
		if err := os.WriteFile(o.PacmanDBLock, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		code = helper.Run([]string{steps.VerbPkgInstall, plan.Hash, "foo"}, o)
		if code != helper.ExitBusy {
			t.Fatalf("exit %d, want busy", code)
		}
		var ref *steps.PkgRefusal
		if _, err := parse(steps.VerbPkgInstall, inst, buf.String(), code); !errors.As(err, &ref) || ref.Reason != steps.RefuseBusy {
			t.Errorf("err = %v, want busy", err)
		}
		if len(p.streams) != 0 {
			t.Errorf("ran %v with db.lck present", p.streams)
		}
	})
}

// TestHelperStaleThroughClientParser: a root run whose confirmed plan no
// longer holds ran nothing and reports the new plan before the result; the
// client reads it as a stale refusal that carries that plan, valid and
// different, for a re-preview.
func TestHelperStaleThroughClientParser(t *testing.T) {
	for _, c := range pkgRequests {
		t.Run(c.name, func(t *testing.T) {
			p := newPkgFake(t)
			pcode, out := p.run(append([]string{planVerb(c.req)}, c.args...)...)
			old, err := parse(planVerb(c.req), c.req, out, pcode)
			if err != nil {
				t.Fatal(err)
			}
			if c.req.Mode == steps.PkgInstall {
				p.planItems = append(p.planItems, "extra surprise 9-1") // the world moved on
			} else {
				p.rmItems = append(p.rmItems, "surprise 9-1")
			}
			argv, err := helperclient.Argv(steps.Step{ID: "x", Verb: runVerb(c.req), Items: old.Targets, PkgPlan: &old})
			if err != nil {
				t.Fatal(err)
			}
			code, out := p.run(helperArgs(t, argv)...)
			if code != helper.ExitStale || len(p.streams) != 0 {
				t.Fatalf("exit %d, ran %v: %s", code, p.streams, out)
			}
			_, err = parse(runVerb(c.req), c.req, out, code)
			var ref *steps.PkgRefusal
			if !errors.As(err, &ref) || ref.Reason != steps.RefuseStale {
				t.Fatalf("err = %v, want stale\n%s", err, out)
			}
			np := ref.Plan
			if np == nil || np.Hash == old.Hash || np.Hash != helperclient.PkgPlanHash(*np) || len(np.Rows) != len(old.Rows)+1 ||
				np.Mode != old.Mode || np.AsDeps != old.AsDeps || np.WithConfig != old.WithConfig {
				t.Errorf("new plan %+v vs old %+v", np, old)
			}
			if err := helperclient.CheckPkgPlan(*np); err != nil {
				t.Errorf("the new plan is not valid: %v", err)
			}
		})
	}
}
