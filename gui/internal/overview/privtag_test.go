package overview

import (
	"testing"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/steps"
)

// Batch 7 (Dev 2, #102): the only step that needs privilege without a root
// helper verb, so the only one tagged "sudo" (runview.PrivTag), is topgrade,
// in every static plan under both auths, and the preview's Root and Sudo
// are exactly the tag counts.
func TestSudoStepIsOnlyTopgrade(t *testing.T) {
	for _, polkit := range []bool{true, false} {
		for _, hyprpm := range []bool{true, false} {
			env := previewEnv(t, polkit, hyprpm)
			for _, c := range Cards() {
				a, ok := actions.ByID(c.ID)
				if !ok || a.Steps == nil {
					continue
				}
				root, sudo := 0, 0
				for _, s := range a.Steps(env) {
					switch {
					case s.NeedsPriv && s.Verb != "":
						root++
					case s.NeedsPriv:
						sudo++
						if s.ID != steps.IDTopgrade {
							t.Errorf("%s (polkit %v): %q needs privilege with no verb and is not topgrade", c.ID, polkit, s.ID)
						}
					}
				}
				p, _ := PlanPreview(env, c.ID)
				if p.Root != root || p.Sudo != sudo {
					t.Errorf("%s (polkit %v): preview root %d sudo %d, steps %d and %d", c.ID, polkit, p.Root, p.Sudo, root, sudo)
				}
			}
		}
	}
}
