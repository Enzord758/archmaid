package ui

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/gui/internal/overview"
	"github.com/Enzord758/archmaid/gui/internal/runview"
	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/steps"
)

var (
	noteCount   = regexp.MustCompile(`^(\d+) steps? runs? as root`)
	kickerCount = regexp.MustCompile(`(\d+) root steps?`)
)

// TestGateNoteMatrix (QA b8 tail): every combination of polkit or sudo,
// kept or unkept password, dry or real, with or without topgrade, with or
// without the orphan probe. The note's root count is always the Overview
// kicker's for the same plan, no note says "each through sudo" beside
// "authorisation", and the wording follows the auth mode alone.
func TestGateNoteMatrix(t *testing.T) {
	for _, polkit := range []bool{true, false} {
		env := actions.Env{
			Polkit: polkit,
			PlanOrphans: func(context.Context) (steps.OrphanPlan, error) {
				return steps.OrphanPlan{}, errors.New("not in a preview")
			},
			LookPath:  func(string) (string, error) { return "", errors.New("not found") },
			AuditStep: func() steps.Step { return steps.Step{ID: steps.IDAURAudit, Title: "Auditing AUR packages…"} },
		}
		prev, ok := overview.PlanPreview(env, actions.IDFullCleanup)
		if !ok {
			t.Fatal("no preview")
		}
		full := gatePlan(t, env)
		for _, topgrade := range []bool{true, false} {
			for _, orphans := range []bool{true, false} {
				plan := slices.Clone(full)
				if !topgrade {
					plan = slices.DeleteFunc(plan, func(s engine.PlanStep) bool { return s.ID == steps.IDTopgrade })
				}
				if !orphans {
					plan = slices.DeleteFunc(plan, func(s engine.PlanStep) bool { return s.ID == steps.IDOrphansProbe })
				}
				for _, kept := range []bool{true, false} {
					for _, dry := range []bool{true, false} {
						name := fmt.Sprintf("polkit=%v/kept=%v/dry=%v/topgrade=%v/orphans=%v", polkit, kept, dry, topgrade, orphans)
						t.Run(name, func(t *testing.T) {
							notes := runview.GateNotes(plan, polkit, kept, dry)
							if len(notes) == 0 {
								t.Fatal("no notes")
							}
							for _, n := range notes {
								if strings.Contains(n, "each through sudo") && strings.Contains(n, "authorisation") {
									t.Errorf("note %q says sudo for each step and authorisation", n)
								}
								if strings.Contains(n, "each through sudo") && polkit {
									t.Errorf("polkit note %q says sudo for the helper's steps", n)
								}
								if !polkit && strings.Contains(n, "root, together") {
									t.Errorf("sudo note %q says the root steps run together", n)
								}
							}
							first := notes[0]
							if dry {
								if first != runview.DryNoPassword {
									t.Errorf("dry note %q, want %q", first, runview.DryNoPassword)
								}
								return
							}
							m := noteCount.FindStringSubmatch(first)
							if m == nil {
								t.Fatalf("no root count in %q", first)
							}
							n, _ := strconv.Atoi(m[1])
							// the plan's root-tagged steps, counted here from the plan itself
							want := 0
							for _, s := range plan {
								if s.NeedsPriv && s.Verb != "" {
									want++
								}
							}
							if n != want {
								t.Errorf("note says %d root steps, the plan has %d tagged root: %q", n, want, first)
							}
							// The Overview's kicker for the same plan: the plan is the preview's
							// unless topgrade or the probe was cut (neither is a root step).
							km := kickerCount.FindStringSubmatch(heroKicker(prev, true, false))
							if km == nil {
								t.Fatalf("no root count in the kicker %q", heroKicker(prev, true, false))
							}
							if kn, _ := strconv.Atoi(km[1]); kn != n {
								t.Errorf("note %d root steps, kicker %d", n, kn)
							}
							if topgrade && !strings.Contains(first, "topgrade gets root through its own sudo") {
								t.Errorf("note %q does not name topgrade's own sudo", first)
							}
							if !topgrade && strings.Contains(first, "topgrade") {
								t.Errorf("note %q names topgrade, which is not in the plan", first)
							}
							// polkit alone asks (no topgrade, so no sudo in the plan):
							// polkit's own wording, no "you type once".
							polkitOnly := polkit && !topgrade
							if kept && !polkitOnly && !strings.Contains(first, "You type your password once") {
								t.Errorf("kept note %q lacks the one-password sentence", first)
							}
							if kept && polkitOnly && (strings.Contains(first, "You type your password") || !strings.Contains(first, "polkit asks for your password")) {
								t.Errorf("polkit-only kept note %q: want polkit's wording, no sudo sentence", first)
							}
							if hasOrph := slices.ContainsFunc(notes, func(s string) bool { return strings.HasPrefix(s, "Orphans are listed") }); hasOrph != orphans {
								t.Errorf("orphans note present = %v, orphan probe in plan = %v", hasOrph, orphans)
							}
						})
					}
				}
			}
		}
	}
}
