package engine_test

import (
	"errors"
	"testing"

	. "github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/engine/enginetest"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/logstore"
)

// The last-run record's outcome follows a precedence (record's doc):
// cancelled when no step started, then aborted, then failed, then
// incomplete (a step refused privilege), then ok. Each case below holds
// two or more of these at once, so swapping any two arms of record's
// switch fails at least one of them. StepsSkipped counts every step that
// didn't run to its end (skipped, refused, aborted, left pending);
// StepsRefused only the refused ones.
func TestSessionRecordOutcomePrecedence(t *testing.T) {
	boom := errors.New("exit status 1")
	// notGranted ends the running (polkit) step refused.
	notGranted := func(h *harness) Output {
		return h.handle(StepExited{Seq: h.seq(), Err: errors.New("exit status 127"), NotGranted: true})
	}
	goOn := child("y", "true")
	goOn.Confirm = "Go on?" // required: declining it stops the queue

	for _, c := range []struct {
		name string
		opts Options
		menu actions.Action
		// drive takes the started action to its end and returns the
		// Output with its Finished.
		drive                    func(h *harness) Output
		want                     string
		failed, skipped, refused int
	}{
		{
			name: "ok",
			menu: queue("q", child("a", "true"), child("b", "true")),
			drive: func(h *harness) Output {
				h.ok()
				return h.ok()
			},
			want: logstore.OutcomeOK,
		},
		{
			name: "incomplete outranks ok",
			opts: Options{Auth: AuthPolkit},
			menu: queue("q", sudo(child("p", "x")), child("b", "true")),
			drive: func(h *harness) Output {
				notGranted(h)
				return h.ok()
			},
			want: logstore.OutcomeIncomplete, skipped: 1, refused: 1,
		},
		{
			name: "failed outranks incomplete (failed first)",
			opts: Options{Auth: AuthPolkit},
			menu: queue("q", child("a", "false"), sudo(child("p", "x")), child("b", "true")),
			drive: func(h *harness) Output {
				h.exit(boom)
				notGranted(h)
				return h.ok()
			},
			want: logstore.OutcomeFailed, failed: 1, skipped: 1, refused: 1,
		},
		{
			name: "failed outranks incomplete (refused first)",
			opts: Options{Auth: AuthPolkit},
			menu: queue("q", sudo(child("p", "x")), child("a", "false")),
			drive: func(h *harness) Output {
				notGranted(h)
				return h.exit(boom)
			},
			want: logstore.OutcomeFailed, failed: 1, skipped: 1, refused: 1,
		},
		{
			name: "aborted outranks failed",
			menu: queue("q", child("a", "false"), child("b", "sleep"), child("c", "true")),
			drive: func(h *harness) Output {
				h.exit(boom)
				return h.abort() // b running, c pending
			},
			want: logstore.OutcomeAborted, failed: 1, skipped: 2,
		},
		{
			name: "aborted outranks incomplete",
			opts: Options{Auth: AuthPolkit},
			menu: queue("q", sudo(child("p", "x")), child("b", "sleep"), child("c", "true")),
			drive: func(h *harness) Output {
				notGranted(h)
				return h.abort()
			},
			want: logstore.OutcomeAborted, skipped: 3, refused: 1,
		},
		{
			name: "aborted (a declined required step) outranks failed and incomplete",
			opts: Options{Auth: AuthPolkit},
			menu: queue("q", child("a", "false"), sudo(child("p", "x")), goOn, child("z", "true")),
			drive: func(h *harness) Output {
				h.exit(boom)
				notGranted(h)
				return h.confirm(false) // y and z skipped
			},
			want: logstore.OutcomeAborted, failed: 1, skipped: 3, refused: 1,
		},
		{
			name: "cancelled outranks aborted (Abort at the first step's confirm)",
			menu: queue("q", goOn, child("z", "true")),
			drive: func(h *harness) Output {
				return h.abort()
			},
			want: logstore.OutcomeCancelled, skipped: 2,
		},
		{
			name: "cancelled outranks aborted (the first step's confirm declined)",
			menu: queue("q", goOn, child("z", "true")),
			drive: func(h *harness) Output {
				return h.confirm(false)
			},
			want: logstore.OutcomeCancelled, skipped: 2,
		},
		{
			name: "cancelled outranks incomplete (three wrong passwords, every step refused)",
			menu: func() actions.Action {
				a := queue("q", sudo(child("p", "sudo", "x")), sudo(child("r", "sudo", "y")))
				a.Gate = "Proceed?"
				return a
			}(),
			drive: func(h *harness) Output {
				// toPrompt has already started the action.
				var o Output
				for range 3 {
					h.password("nope")
					o = h.handle(AuthDone{Err: errors.New("wrong")})
				}
				return o
			},
			want: logstore.OutcomeCancelled, skipped: 2, refused: 2,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t, c.opts, c.menu)
			if c.menu.Gate != "" {
				h.toPrompt("q")
			} else {
				h.start("q")
			}
			o := c.drive(h)
			event[Finished](t, o)
			rec := effect[WriteLastRun](t, o).Record
			if rec.Outcome != c.want || rec.StepsFailed != c.failed || rec.StepsSkipped != c.skipped || rec.StepsRefused != c.refused {
				t.Errorf("record: outcome %q, failed %d, skipped %d, refused %d; want %q, %d, %d, %d",
					rec.Outcome, rec.StepsFailed, rec.StepsSkipped, rec.StepsRefused,
					c.want, c.failed, c.skipped, c.refused)
			}
			enginetest.CheckContract(t, h.evs)
		})
	}
}
