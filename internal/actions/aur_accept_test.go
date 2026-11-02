package actions

import (
	"testing"

	"github.com/Enzord758/archmaid/internal/steps"
)

// TestAURDiscoverAcceptIsTheFirstStepsOnAccept: AURPrepared.Accept rides on
// the queue's first step (with or without a repo-dependency step), and only
// there; without it no step has an OnAccept.
func TestAURDiscoverAcceptIsTheFirstStepsOnAccept(t *testing.T) {
	for _, repoDeps := range [][]string{nil, {"libfoo"}} {
		var calls []string
		var deps []*steps.Step
		n := 0
		prep := AURPrepared{Plans: aurPlans, RepoDeps: repoDeps, Queue: queueOf(&deps), Accept: func() error { n++; return nil }}
		pv, ok := discover(aurEnv(prep, nil, &calls), aurReq()).(PkgPreview)
		if !ok || len(pv.Steps) < 1 || pv.Steps[0].OnAccept == nil {
			t.Fatalf("deps %v: %+v", repoDeps, pv.Steps)
		}
		for i, st := range pv.Steps[1:] {
			if st.OnAccept != nil {
				t.Errorf("step %d has an OnAccept too", i+1)
			}
		}
		pv.Steps[0].OnAccept()
		if n != 1 {
			t.Errorf("accept ran %d times", n)
		}

		prep.Accept = nil
		pv = discover(aurEnv(prep, nil, &calls), aurReq()).(PkgPreview)
		for _, st := range pv.Steps {
			if st.OnAccept != nil {
				t.Errorf("deps %v: %s has an OnAccept without an Accept", repoDeps, st.ID)
			}
		}
	}
}
