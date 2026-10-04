package helperclient

import (
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/steps"
)

func manyNames(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("pkg%d", i)
	}
	return out
}

// TestArgv: each verb's exact command line, from the step's typed fields.
// The sudo Argv is set to something else on purpose: it is never read.
func TestArgv(t *testing.T) {
	junk := []string{"sudo", "rm", "-rf", "/"}
	cases := []struct {
		name string
		st   steps.Step
		want []string
	}{
		{"full check", steps.Step{Verb: steps.VerbCheckFiles, Argv: junk},
			[]string{"pkexec", HelperPath, "check-files"}},
		{"named check", actions.RepairCheckStep([]string{"amd-ucode", "nfs-utils"}),
			[]string{"pkexec", HelperPath, "check-files", "amd-ucode", "nfs-utils"}},
		{"repair", actions.RepairStep([]string{"a", "b"}),
			[]string{"pkexec", HelperPath, "repair", "a", "b"}},
		{"disable-unit", actions.DisableUnits([]string{"cups.service"})[0],
			[]string{"pkexec", HelperPath, "disable-unit", "cups.service"}},
		{"paccache", steps.Step{Verb: steps.VerbPaccache, Argv: junk},
			[]string{"pkexec", HelperPath, "paccache", "3"}},
		{"vacuum-journal", steps.Step{Verb: steps.VerbVacuumJournal, Argv: junk},
			[]string{"pkexec", HelperPath, "vacuum-journal", "2weeks"}},
		{"paccache-wipe", steps.Step{Verb: steps.VerbPaccacheWipe, Argv: junk},
			[]string{"pkexec", HelperPath, "paccache-wipe"}},
		{"clean-tmp", steps.Step{Verb: steps.VerbCleanTmp, Argv: junk},
			[]string{"pkexec", HelperPath, "clean-tmp"}},
		{"remove-orphans", steps.Step{Verb: steps.VerbRemoveOrphans, Argv: junk, Items: goodDirect, Cascade: goodCascade,
			Plan: &steps.OrphanPlan{Hash: PlanHash(goodDirect, goodCascade), Direct: goodDirect, Cascade: goodCascade}},
			[]string{"pkexec", HelperPath, "remove-orphans", PlanHash(goodDirect, goodCascade)}},
		{"256 names", steps.Step{Verb: steps.VerbRepair, Items: manyNames(256)},
			append([]string{"pkexec", HelperPath, "repair"}, manyNames(256)...)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Argv(c.st)
			if err != nil || !slices.Equal(got, c.want) {
				t.Fatalf("Argv = %q, %v; want %q", got, err, c.want)
			}
		})
	}
}

// TestArgvRefused: what the helper would refuse (or can't be run through
// pkexec) is refused before anything starts, so no password is asked for
// a call that can only fail.
func TestArgvRefused(t *testing.T) {
	cases := []struct {
		name string
		st   steps.Step
		want error
	}{
		{"no verb", steps.Step{ID: "x", Argv: []string{"sudo", "true"}}, ErrNoVerb},
		{"plan-orphans", steps.Step{Verb: steps.VerbPlanOrphans}, ErrNoAction},
		{"remove-orphans has no typed hash", steps.Step{Verb: steps.VerbRemoveOrphans, Items: []string{"a"}}, ErrUnsupported},
		{"unknown verb", steps.Step{Verb: "rm"}, ErrUnsupported},
		{"full-cleanup", steps.Step{Verb: "full-cleanup"}, ErrUnsupported},
		{"repair without names", steps.Step{Verb: steps.VerbRepair}, ErrBadItems},
		{"repair 257 names", steps.Step{Verb: steps.VerbRepair, Items: manyNames(257)}, ErrBadItems},
		{"repair flag", steps.Step{Verb: steps.VerbRepair, Items: []string{"a", "--root=/"}}, ErrBadItems},
		{"repair dash", steps.Step{Verb: steps.VerbRepair, Items: []string{"-x"}}, ErrBadItems},
		{"repair empty", steps.Step{Verb: steps.VerbRepair, Items: []string{""}}, ErrBadItems},
		{"repair twice", steps.Step{Verb: steps.VerbRepair, Items: []string{"a", "a"}}, ErrBadItems},
		{"repair space", steps.Step{Verb: steps.VerbRepair, Items: []string{"a b"}}, ErrBadItems},
		{"repair shell", steps.Step{Verb: steps.VerbRepair, Items: []string{"a;rm"}}, ErrBadItems},
		{"repair bidi", steps.Step{Verb: steps.VerbRepair, Items: []string{"a‮b"}}, ErrBadItems},
		{"check-files path", steps.Step{Verb: steps.VerbCheckFiles, Items: []string{"../../etc/shadow"}}, ErrBadItems},
		{"unit not a service", steps.Step{Verb: steps.VerbDisableUnit, Items: []string{"a.timer"}}, ErrBadItems},
		{"unit flag", steps.Step{Verb: steps.VerbDisableUnit, Items: []string{"--now.service"}}, nil},
		{"unit none", steps.Step{Verb: steps.VerbDisableUnit}, ErrBadItems},
		{"paccache with items", steps.Step{Verb: steps.VerbPaccache, Items: []string{"0"}}, ErrBadItems},
		{"clean-tmp with items", steps.Step{Verb: steps.VerbCleanTmp, Items: []string{"x"}}, ErrBadItems},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Argv(c.st)
			if c.want == nil {
				// "--now.service" matches the helper's unit pattern; it
				// still lands after the verb, where the helper puts "--"
				// before it. Pinned so a change to the pattern is seen.
				if err != nil {
					t.Fatalf("Argv = %v, want accepted like the helper does", err)
				}
				return
			}
			if !errors.Is(err, c.want) || got != nil {
				t.Fatalf("Argv = %q, %v; want %v", got, err, c.want)
			}
		})
	}
}

// TestArgvDoesNotShareItems: the argv is the step's names, copied.
func TestArgvDoesNotShareItems(t *testing.T) {
	st := steps.Step{Verb: steps.VerbRepair, Items: []string{"a"}}
	argv, _ := Argv(st)
	argv[3] = "evil"
	if st.Items[0] != "a" {
		t.Fatal("Argv shares the step's Items")
	}
}

const policyFile = "../../packaging/polkit/org.archmaid.helper.policy"

type policy struct {
	Actions []struct {
		ID       string `xml:"id,attr"`
		Annotate []struct {
			Key   string `xml:"key,attr"`
			Value string `xml:",chardata"`
		} `xml:"annotate"`
	} `xml:"action"`
}

// TestArgvMatchesPolicy: for every polkit action, the command Argv builds
// for its verb is the one the action matches, exec.path then exec.argv1;
// every verb Argv accepts has such an action; and no action lacks argv1
// (an action without one can take calls meant for another,
// docs/polkit-live-test.md).
func TestArgvMatchesPolicy(t *testing.T) {
	data, err := os.ReadFile(policyFile)
	if err != nil {
		t.Fatal(err)
	}
	var p policy
	if err := xml.Unmarshal(data, &p); err != nil {
		t.Fatal(err)
	}
	sample := map[string][]string{
		steps.VerbRepair:      {"a"},
		steps.VerbDisableUnit: {"a.service"},
	}
	actionVerbs := map[string]bool{}
	for _, a := range p.Actions {
		ann := map[string]string{}
		for _, an := range a.Annotate {
			ann[an.Key] = an.Value
		}
		path, argv1 := ann["org.freedesktop.policykit.exec.path"], ann["org.freedesktop.policykit.exec.argv1"]
		if argv1 == "" {
			t.Errorf("%s: no exec.argv1", a.ID)
			continue
		}
		actionVerbs[argv1] = true
		st := steps.Step{Verb: argv1, Items: sample[argv1]}
		if argv1 == steps.VerbRemoveOrphans {
			p := goodPlan()
			st.Items, st.Cascade, st.Plan = p.Direct, p.Cascade, &p
		}
		got, err := Argv(st)
		if errors.Is(err, ErrUnsupported) {
			continue // full-cleanup: not built from a step yet
		}
		if err != nil {
			t.Errorf("%s: Argv: %v", a.ID, err)
			continue
		}
		if got[0] != Pkexec || got[1] != path || got[2] != argv1 {
			t.Errorf("%s: Argv %q, want pkexec %s %s …", a.ID, got, path, argv1)
		}
	}
	for _, v := range []string{steps.VerbCheckFiles, steps.VerbRepair, steps.VerbDisableUnit, steps.VerbPaccache,
		steps.VerbVacuumJournal, steps.VerbPaccacheWipe, steps.VerbCleanTmp, steps.VerbRemoveOrphans} {
		if !actionVerbs[v] {
			t.Errorf("Argv builds %s, but the policy has no action with that argv1", v)
		}
	}
	if actionVerbs[steps.VerbPlanOrphans] {
		t.Error("the policy has a plan-orphans action, but Argv refuses the verb")
	}
}

// TestFaillock: the wrapper hands privilege.FaillockStatus's error on; a
// user with no tally file has no failures.
func TestFaillock(t *testing.T) {
	if r := Faillock("../etc", time.Now()); r.Err == nil {
		t.Errorf("Faillock(invalid user) = %+v, want an error", r)
	}
	r := Faillock("archmaid-test-no-such-user-91c2", time.Now())
	if r.Err != nil || r.Failures != 0 {
		t.Errorf("Faillock(no tally) = %+v, want no failures", r)
	}
}
